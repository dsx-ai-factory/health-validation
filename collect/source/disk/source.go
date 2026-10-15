// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package disk reads block inventory, mount metadata, and filesystem capacity.
package disk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
)

var attributePaths = map[collect.Signal]string{signals.Serial: "device/serial", signals.WWN: "wwid", signals.Model: "device/model", signals.Vendor: "device/vendor", signals.Revision: "device/rev", signals.Rotational: "queue/rotational", signals.SizeBytes: "size"}

const Name = "disk.sysfs"

var (
	ErrInUse       = errors.New("disk source call already in progress")
	ErrUnsupported = errors.New("operation not supported")
)

type Reading struct {
	Signal  collect.Signal
	Target  *observation.ObservationTarget
	Value   *observation.Value
	Failure *observation.CollectionError
	NoValue *observation.NoValue
}

type (
	Usage struct{ Total, Free, Used uint64 }
	// StatfsFunc performs read-only capacity acquisition in the injected mount view.
	// The caller owns identity alignment with the injected mountinfo. Cancellation
	// cannot stop a kernel syscall; Source admits at most one outstanding invocation.
	StatfsFunc func(string) (Usage, error)
	Option     func(*Source)
)

func WithNamespace(namespace string) Option { return func(s *Source) { s.namespace = namespace } }
func WithHostRoot(root string) Option       { return func(s *Source) { s.root = root } }

// WithMountPID selects a process in the injected procfs whose mount namespace
// matches WithHostRoot. PID 1 is not assumed to be the host process.
func WithMountPID(pid uint32) Option    { return func(s *Source) { s.mountPID = pid } }
func WithStatfs(call StatfsFunc) Option { return func(s *Source) { s.statfs = call } }
func WithStatfsTimeout(timeout time.Duration) Option {
	return func(s *Source) {
		if timeout > 0 {
			s.timeout = timeout
		}
	}
}
func WithMountFilter(filter func(Mount) bool) Option { return func(s *Source) { s.filter = filter } }

type Source struct {
	files     fs.FS
	statfs    StatfsFunc
	filter    func(Mount) bool
	namespace string
	root      string
	timeout   time.Duration
	mountPID  uint32
	mu        sync.Mutex
	statMu    sync.Mutex
}

// New uses a host-root view. Injected files do not implicitly use live host
// statfs; supply WithHostRoot or WithStatfs for that separate acquisition.
func New(files fs.FS, opts ...Option) *Source {
	s := &Source{files: files, timeout: 2 * time.Second}
	if files == nil {
		s.files = os.DirFS("/")
		s.root = "/"
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func Failure(err error) *observation.CollectionError {
	category := observation.CollectionErrorCategorySourceError
	var numberError *strconv.NumError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, ErrUnsupported):
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, ErrInUse):
		category = observation.CollectionErrorCategoryUnavailable
	case errors.As(err, &numberError):
		category = observation.CollectionErrorCategoryParseError
	}
	return &observation.CollectionError{Category: category, Detail: err.Error()}
}

func Uint(value uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}
}

func Text(value string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: value}}
}

func failed(signal collect.Signal, target *observation.ObservationTarget, err error, operation string) Reading {
	failure := Failure(err)
	failure.Operation = operation
	return Reading{Signal: signal, Target: target, Failure: failure}
}

func missing(signal collect.Signal) Reading {
	return Reading{Signal: signal, NoValue: &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "no matching entities"}}
}

func (s *Source) Read(ctx context.Context, selected collect.Signals) ([]Reading, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	selected, err := collect.NewOp(collect.WithSignalID(selected.IDs()...)).Signals(signals.All())
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	if !s.mu.TryLock() {
		return nil, ErrInUse
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var blocks, mounts collect.Signals
	for _, signal := range selected {
		if signals.IsMount(signal) {
			mounts = append(mounts, signal)
		} else {
			blocks = append(blocks, signal)
		}
	}
	var out []Reading
	if len(blocks) > 0 {
		out = append(out, s.readBlocks(ctx, blocks)...)
	}
	if len(mounts) > 0 {
		out = append(out, s.readMounts(ctx, mounts)...)
	}
	return out, nil
}

func (s *Source) readBlocks(ctx context.Context, selected collect.Signals) []Reading {
	entries, err := fs.ReadDir(s.files, "sys/class/block")
	if err != nil {
		var out []Reading
		for _, signal := range selected {
			out = append(out, failed(signal, nil, err, "sys/class/block"))
		}
		return out
	}
	var out []Reading
	for _, entry := range entries {
		base := path.Join("sys/class/block", entry.Name())
		raw, err := fs.ReadFile(s.files, path.Join(base, "dev"))
		var major, minor uint32
		if err == nil {
			major, minor, err = ParseDevice(string(raw))
			if err != nil {
				err = parseError(err)
			}
		}
		if err != nil {
			for _, signal := range selected {
				out = append(out, failed(signal, nil, err, path.Join(base, "dev")))
			}
			continue
		}
		target := observation.BlockDeviceTarget(major, minor)
		for _, signal := range selected {
			reading := Reading{Signal: signal, Target: target}
			if err := ctx.Err(); err != nil {
				out = append(out, failed(signal, target, err, base))
				continue
			}
			switch signal {
			case signals.Name:
				reading.Value = Text(entry.Name())
			case signals.Major:
				reading.Value = Uint(uint64(major))
			case signals.Minor:
				reading.Value = Uint(uint64(minor))
			case signals.Type:
				_, err = fs.Stat(s.files, path.Join(base, "partition"))
				if err == nil {
					reading.Value = Text("partition")
				} else if errors.Is(err, fs.ErrNotExist) {
					kind := "disk"
					if strings.HasPrefix(entry.Name(), "dm-") {
						kind = "device_mapper"
					} else if strings.HasPrefix(entry.Name(), "md") {
						kind = "raid"
					} else if strings.HasPrefix(entry.Name(), "loop") {
						kind = "loop"
					}
					reading.Value = Text(kind)
				} else {
					reading.Failure = Failure(err)
				}
			case signals.Parents:
				reading.Value, err = s.parents(base)
				if err != nil {
					reading.Failure = Failure(err)
				}
			default:
				field := attributePaths[signal]
				raw, err := s.attribute(base, field)
				if err != nil {
					reading = failed(signal, target, err, path.Join(base, field))
					break
				}
				text := strings.TrimSpace(string(raw))
				if signal == signals.SizeBytes || signal == signals.Rotational {
					value, err := strconv.ParseUint(text, 10, 64)
					if err == nil && signal == signals.SizeBytes {
						if value > math.MaxUint64/512 {
							err = parseError(fmt.Errorf("sector count overflows bytes"))
						} else {
							value *= 512
						}
					}
					if err == nil && signal == signals.Rotational && value > 1 {
						err = parseError(fmt.Errorf("rotational must be 0 or 1"))
					}
					if err != nil {
						reading = failed(signal, target, err, path.Join(base, field))
					} else if signal == signals.Rotational {
						reading.Value = &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: value == 1}}
					} else {
						reading.Value = Uint(value)
					}
				} else if text == "" {
					reading = failed(signal, target, fmt.Errorf("empty %s: %w", field, ErrUnsupported), path.Join(base, field))
				} else {
					reading.Value = Text(text)
				}
			}
			validateTextReading(&reading)
			out = append(out, reading)
		}
	}
	if len(entries) == 0 {
		for _, signal := range selected {
			out = append(out, missing(signal))
		}
	}
	return out
}

func parseError(err error) error { return &strconv.NumError{Func: "parse", Err: err} }

func validateTextReading(reading *Reading) {
	if value, ok := reading.Value.GetKind().(*observation.Value_StringValue); ok && !utf8.ValidString(value.StringValue) {
		reading.Value = nil
		reading.Failure = Failure(parseError(fmt.Errorf("non-UTF-8 disk metadata")))
	}
}

func (s *Source) attribute(base, field string) ([]byte, error) {
	raw, err := fs.ReadFile(s.files, path.Join(base, field))
	if !errors.Is(err, fs.ErrNotExist) {
		return raw, err
	}
	if field == "wwid" {
		raw, err = fs.ReadFile(s.files, path.Join(base, "device/wwid"))
		if !errors.Is(err, fs.ErrNotExist) {
			return raw, err
		}
	}
	if field == "size" {
		return nil, err
	}
	if _, partitionErr := fs.Stat(s.files, path.Join(base, "partition")); partitionErr != nil {
		return nil, err
	}
	link, linkErr := fs.ReadLink(s.files, base)
	if linkErr != nil {
		return nil, linkErr
	}
	parent := path.Base(path.Dir(link))
	parentBase := path.Join("sys/class/block", parent)
	raw, err = fs.ReadFile(s.files, path.Join(parentBase, field))
	if errors.Is(err, fs.ErrNotExist) && field == "wwid" {
		return fs.ReadFile(s.files, path.Join(parentBase, "device/wwid"))
	}
	return raw, err
}

func (s *Source) parents(base string) (*observation.Value, error) {
	var parents []string
	_, err := fs.Stat(s.files, path.Join(base, "partition"))
	if err == nil {
		link, err := fs.ReadLink(s.files, base)
		if err != nil {
			return nil, err
		}
		parent := path.Base(path.Dir(link))
		raw, err := fs.ReadFile(s.files, path.Join("sys/class/block", parent, "dev"))
		if err != nil {
			return nil, err
		}
		major, minor, err := ParseDevice(string(raw))
		if err != nil {
			return nil, parseError(err)
		}
		parents = append(parents, fmt.Sprintf("%d:%d", major, minor))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	entries, err := fs.ReadDir(s.files, path.Join(base, "slaves"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		raw, err := fs.ReadFile(s.files, path.Join(base, "slaves", entry.Name(), "dev"))
		if err != nil {
			return nil, err
		}
		major, minor, err := ParseDevice(string(raw))
		if err != nil {
			return nil, parseError(err)
		}
		parents = append(parents, fmt.Sprintf("%d:%d", major, minor))
	}
	slices.Sort(parents)
	return Text(strings.Join(slices.Compact(parents), ",")), nil
}

// Only known local filesystems are eligible for statfs. Never touch autofs,
// FUSE, or network mounts; metadata remains available for those mounts.
func safeCapacity(m Mount) bool {
	switch m.FSType {
	case "ext2", "ext3", "ext4", "xfs", "btrfs", "f2fs", "vfat", "exfat", "tmpfs", "overlay", "zfs":
		return true
	}
	return false
}

func (s *Source) readMounts(ctx context.Context, selected collect.Signals) []Reading {
	mounts, failures, err := ReadMounts(s.files, s.namespace, s.mountPID)
	if err != nil {
		var out []Reading
		for _, signal := range selected {
			out = append(out, failed(signal, nil, err, MountInfoPath(s.mountPID)))
		}
		return out
	}
	var out []Reading
	for _, failure := range failures {
		var target *observation.ObservationTarget
		if failure.ID != 0 && s.namespace != "" {
			target = observation.MountTarget(s.namespace, failure.ID)
		}
		for _, signal := range selected {
			out = append(out, failed(signal, target, parseError(failure.Err), MountInfoPath(s.mountPID)))
		}
	}
	// Mountinfo order does not establish which stacked mount a path resolves to.
	// Preserve every mount's metadata, but do not statfs an ambiguous path.
	pathCount := make(map[string]int)
	for _, mount := range mounts {
		pathCount[mount.Point]++
	}
	count := 0
	for _, m := range mounts {
		if s.filter != nil && !s.filter(m) {
			continue
		}
		count++
		target := observation.MountTarget(m.Namespace, m.ID)
		var usage Usage
		var usageErr error
		acquired := false
		for _, signal := range selected {
			reading := Reading{Signal: signal, Target: target}
			switch signal {
			case signals.MountPoint:
				reading.Value = Text(m.Point)
			case signals.MountRoot:
				reading.Value = Text(m.Root)
			case signals.MountParentID:
				reading.Value = Uint(m.ParentID)
			case signals.MountOptions:
				reading.Value = Text(m.Options)
			case signals.MountOptionalFields:
				reading.Value = Text(m.OptionalFields)
			case signals.MountSuperOptions:
				reading.Value = Text(m.SuperOptions)
			case signals.MountFSType:
				reading.Value = Text(m.FSType)
			case signals.MountSource:
				reading.Value = Text(m.Source)
			case signals.MountDevice:
				reading.Value = Text(fmt.Sprintf("%d:%d", m.Major, m.Minor))
			default:
				if !utf8.ValidString(m.Point) {
					reading = failed(signal, target, parseError(fmt.Errorf("non-UTF-8 mount point")), "statfs")
					break
				}
				if len(failures) > 0 {
					reading = failed(signal, target, parseError(fmt.Errorf("capacity requires complete mountinfo")), MountInfoPath(s.mountPID))
					break
				}
				if !safeCapacity(m) || pathCount[m.Point] != 1 {
					reading.NoValue = &observation.NoValue{Reason: observation.NoValueReasonNotSupported, Detail: "capacity skipped for unsafe or stacked mount"}
					break
				}
				if !acquired {
					usage, usageErr = s.usage(ctx, m)
					acquired = true
				}
				if usageErr != nil {
					reading = failed(signal, target, usageErr, "statfs "+m.Point)
				} else {
					value := usage.Total
					if signal == signals.FreeBytes {
						value = usage.Free
					} else if signal == signals.UsedBytes {
						value = usage.Used
					}
					reading.Value = Uint(value)
				}
			}
			validateTextReading(&reading)
			out = append(out, reading)
		}
	}
	if count == 0 && len(failures) == 0 {
		for _, signal := range selected {
			out = append(out, missing(signal))
		}
	}
	return out
}

func (s *Source) usage(ctx context.Context, mount Mount) (Usage, error) {
	if s.statfs == nil && s.root == "" {
		return Usage{}, fmt.Errorf("statfs host root or injected source is required: %w", ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	if !s.statMu.TryLock() {
		return Usage{}, ErrInUse
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type result struct {
		err   error
		usage Usage
	}
	done := make(chan result, 1)
	go func() {
		current := result{}
		defer func() {
			if recovered := recover(); recovered != nil {
				current.err = fmt.Errorf("statfs panic: %v", recovered)
			}
			s.statMu.Unlock()
			done <- current
		}()
		if s.statfs != nil {
			current.usage, current.err = s.statfs(mount.Point)
		} else if path.Clean(s.root) != "/" && s.mountPID == 0 {
			current.err = fmt.Errorf("non-default host root requires an aligned WithMountPID: %w", ErrUnsupported)
		} else {
			current.usage, current.err = nativeUsage(s.root, mount)
		}
	}()
	select {
	case r := <-done:
		return r.usage, r.err
	case <-callCtx.Done():
		return Usage{}, callCtx.Err()
	}
}
