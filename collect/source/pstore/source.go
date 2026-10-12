// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pstore reads raw records without deleting files or storing history.
package pstore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pstoresignals "github.com/dsx-ai-factory/health-validation/collect/signals/pstore"
)

const (
	Name           = "pstore.files"
	MaxDepth       = 3
	MaxRecordBytes = 16 << 20
	MaxEntries     = 10000
)

var (
	ErrInUse     = errors.New("pstore read already in progress")
	ErrLimit     = errors.New("pstore collection limit exceeded")
	ErrMalformed = errors.New("pstore record is not UTF-8")
)

// Source scans both live and systemd-archived records by default. Paths are
// relative to files; nil uses a host filesystem view rooted at /.
type Source struct {
	files fs.FS
	roots []string
	mu    sync.Mutex
	host  bool
}

func New(files fs.FS, roots ...string) *Source {
	host := files == nil
	if files == nil {
		files = os.DirFS("/")
	}
	if len(roots) == 0 {
		roots = []string{"sys/fs/pstore", "var/lib/systemd/pstore"}
	}
	return &Source{files: files, host: host, roots: append([]string(nil), roots...)}
}

type Reading struct {
	Signal  collect.Signal
	Target  *observation.ObservationTarget
	Value   *observation.Value
	Failure *observation.CollectionError
	NoValue *observation.NoValue
	Path    string
}

// Read hashes each complete record, so identical content at the same path keeps
// its target across scans. Collection time never substitutes for crash time.
func (s *Source) Read(ctx context.Context, selected collect.Signals) ([]Reading, error) {
	selected, err := collect.NewOp(collect.WithSignalID(selected.IDs()...)).Signals(pstoresignals.All())
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if !s.mu.TryLock() {
		return nil, ErrInUse
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var readings []Reading
	seenRoots := make(map[string]bool)
	entriesLeft := MaxEntries
	for _, root := range s.roots {
		if seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		if !utf8.ValidString(root) {
			readings = append(readings, failures(selected, "scan", fmt.Errorf("%w: non-UTF-8 record root", ErrMalformed))...)
			continue
		}
		if !fs.ValidPath(root) || root == "." {
			readings = append(readings, failures(selected, root, fmt.Errorf("%w: root must be a relative directory path", fs.ErrInvalid))...)
			continue
		}
		if err := ctx.Err(); err != nil {
			readings = append(readings, failures(selected, root, err)...)
			continue
		}
		if err := noLinks(s.files, root); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				readings = append(readings, failures(selected, root, err)...)
			}
			continue
		}
		var files fs.FS
		var closeRoot func() error
		if s.host {
			var confined *os.Root
			confined, err = os.OpenRoot("/" + root)
			if err == nil {
				files = confined.FS()
				closeRoot = confined.Close
			}
		} else {
			files, err = fs.Sub(s.files, root)
		}
		if err != nil {
			readings = append(readings, failures(selected, root, err)...)
			continue
		}
		readings = append(readings, s.scan(ctx, files, root, ".", 0, selected, &entriesLeft)...)
		if closeRoot != nil {
			_ = closeRoot()
		}
	}
	if len(readings) == 0 {
		for _, signal := range selected {
			readings = append(readings, Reading{Signal: signal, NoValue: &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "no pstore records in configured roots"}})
		}
	}
	return readings, nil
}

// scan rejects symlinks and non-regular files. The host view uses os.Root so
// concurrent link replacement cannot escape the configured record directory.
func (s *Source) scan(ctx context.Context, files fs.FS, root, directory string, depth int, selected collect.Signals, entriesLeft *int) []Reading {
	fullDirectory := path.Join(root, directory)
	if err := ctx.Err(); err != nil {
		return failures(selected, fullDirectory, err)
	}
	entries, err := fs.ReadDir(files, directory)
	if err != nil {
		return failures(selected, fullDirectory, err)
	}
	var readings []Reading
	for _, entry := range entries {
		name := path.Join(directory, entry.Name())
		fullName := path.Join(root, name)
		if err := ctx.Err(); err != nil {
			readings = append(readings, failures(selected, fullDirectory, err)...)
			break
		}
		if *entriesLeft <= 0 {
			readings = append(readings, failures(selected, fullDirectory, ErrLimit)...)
			break
		}
		*entriesLeft--
		if !utf8.ValidString(fullName) {
			readings = append(readings, failures(selected, fullDirectory, fmt.Errorf("%w: non-UTF-8 record path", ErrMalformed))...)
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			readings = append(readings, failures(selected, fullName, fmt.Errorf("%w: symlinks are not traversed", syscall.EOPNOTSUPP))...)
			continue
		}
		if entry.IsDir() {
			if depth == MaxDepth {
				readings = append(readings, failures(selected, fullName, fmt.Errorf("%w: maximum directory depth %d", ErrLimit, MaxDepth))...)
				continue
			}
			readings = append(readings, s.scan(ctx, files, root, name, depth+1, selected, entriesLeft)...)
			continue
		}
		info, err := entry.Info()
		if err != nil {
			readings = append(readings, failures(selected, fullName, err)...)
			continue
		}
		if !info.Mode().IsRegular() {
			readings = append(readings, failures(selected, fullName, fmt.Errorf("%w: not a regular record", syscall.EOPNOTSUPP))...)
			continue
		}
		if info.Size() > MaxRecordBytes {
			readings = append(readings, failures(selected, fullName, ErrLimit)...)
			continue
		}
		raw, err := readRecord(files, name)
		if err != nil {
			readings = append(readings, failures(selected, fullName, err)...)
			continue
		}
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		target := observation.PstoreRecordTarget(fullName, digest)
		for _, signal := range selected {
			reading := Reading{Signal: signal, Target: target, Path: fullName}
			var value string
			switch signal.ID() {
			case pstoresignals.Path.ID():
				value = fullName
			case pstoresignals.SHA256.ID():
				value = digest
			case pstoresignals.Root.ID():
				value = root
			case pstoresignals.ContentBase64.ID():
				value = base64.StdEncoding.EncodeToString(raw)
			case pstoresignals.RawMessage.ID():
				if !utf8.Valid(raw) {
					reading.Failure = Failure(ErrMalformed)
				} else {
					value = string(raw)
				}
			}
			if reading.Failure == nil {
				reading.Value = &observation.Value{Kind: &observation.Value_StringValue{StringValue: value}}
			}
			readings = append(readings, reading)
		}
	}
	return readings
}

func readRecord(files fs.FS, name string) ([]byte, error) {
	if err := noLinks(files, name); err != nil {
		return nil, err
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular record", syscall.EOPNOTSUPP)
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
	if err == nil && len(raw) > MaxRecordBytes {
		err = ErrLimit
	}
	return raw, err
}

// noLinks checks ancestors as well as the leaf, before generic fs.FS operations.
func noLinks(files fs.FS, name string) error {
	prefix := "."
	for _, component := range strings.Split(name, "/") {
		if component == "." {
			continue
		}
		entries, err := fs.ReadDir(files, prefix)
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() != component {
				continue
			}
			found = true
			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%w: symlink at %s", syscall.EOPNOTSUPP, path.Join(prefix, component))
			}
			break
		}
		prefix = path.Join(prefix, component)
		if !found {
			return &fs.PathError{Op: "enumerate", Path: prefix, Err: fs.ErrNotExist}
		}
	}
	return nil
}

func failures(selected collect.Signals, name string, err error) []Reading {
	readings := make([]Reading, 0, len(selected))
	for _, signal := range selected {
		readings = append(readings, Reading{Signal: signal, Target: observation.NodeTarget(""), Path: name, Failure: Failure(err)})
	}
	return readings
}

func Failure(err error) *observation.CollectionError {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, ErrMalformed):
		category = observation.CollectionErrorCategoryParseError
	case errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, fs.ErrInvalid):
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrInUse), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled):
		category = observation.CollectionErrorCategoryUnavailable
	}
	return &observation.CollectionError{Category: category, Detail: err.Error()}
}
