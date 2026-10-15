// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package fuse reads FUSE control queues without touching mounted filesystems.
package fuse

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/fuse"
	"github.com/dsx-ai-factory/health-validation/collect/source/disk"
)

const Name = "fuse.sysfs"

var (
	ErrInUse   = errors.New("FUSE source call already in progress")
	queuePaths = map[collect.Signal]string{signals.Waiting: "waiting", signals.MaxBackground: "max_background", signals.CongestionThreshold: "congestion_threshold"}
)

type (
	Reading = disk.Reading
	Source  struct {
		files     fs.FS
		namespace string
		mountPID  uint32
		mu        sync.Mutex
	}
	Option func(*Source)
)

func WithNamespace(namespace string) Option { return func(s *Source) { s.namespace = namespace } }

// WithMountPID selects a process in the supplied procfs for mount correlation.
// Its mount namespace must be the caller's intended host view.
func WithMountPID(pid uint32) Option { return func(s *Source) { s.mountPID = pid } }

func New(files fs.FS, opts ...Option) *Source {
	if files == nil {
		files = os.DirFS("/")
	}
	s := &Source{files: files}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func Failure(err error) *observation.CollectionError {
	if errors.Is(err, ErrInUse) {
		return &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: err.Error()}
	}
	return disk.Failure(err)
}

func failure(signal collect.Signal, target *observation.ObservationTarget, err error, operation string) Reading {
	f := Failure(err)
	f.Operation = operation
	return Reading{Signal: signal, Target: target, Failure: f}
}

func absent(signal collect.Signal) Reading {
	return Reading{Signal: signal, NoValue: &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "no visible FUSE connections"}}
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	entries, err := fs.ReadDir(s.files, "sys/fs/fuse/connections")
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0 {
		var out []Reading
		for _, signal := range selected {
			out = append(out, absent(signal))
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	boot, err := fs.ReadFile(s.files, "proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	bootID := strings.TrimSpace(string(boot))
	if bootID == "" {
		return nil, &strconv.NumError{Func: "boot_id", Err: fmt.Errorf("empty boot identity")}
	}
	var mounts []disk.Mount
	var mountFailures []disk.MountFailure
	var mountErr error
	needMounts := false
	for _, signal := range selected {
		needMounts = needMounts || signals.IsMount(signal)
	}
	if needMounts {
		mounts, mountFailures, mountErr = disk.ReadMounts(s.files, s.namespace, s.mountPID)
	}
	var out []Reading
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := connectionID(entry.Name())
		if err != nil {
			for _, signal := range selected {
				out = append(out, failure(signal, nil, err, "sys/fs/fuse/connections/"+entry.Name()))
			}
			continue
		}
		count++
		target := observation.FUSEConnectionTarget(bootID, id)
		var matches []disk.Mount
		for _, mount := range mounts {
			// fusectl names fc->dev (the kernel dev_t), including fuseblk devices.
			// include/linux/kdev_t.h encodes dev_t as major<<20 | minor.
			deviceID := uint64(mount.Major)<<20 | uint64(mount.Minor)
			if mount.Major < 1<<12 && mount.Minor < 1<<20 && deviceID == id && (mount.FSType == "fuse" || mount.FSType == "fuseblk" || strings.HasPrefix(mount.FSType, "fuse.")) {
				matches = append(matches, mount)
			}
		}
		for _, signal := range selected {
			if err := ctx.Err(); err != nil {
				out = append(out, failure(signal, target, err, Name))
				continue
			}
			if signals.IsMount(signal) {
				if mountErr != nil {
					out = append(out, failure(signal, target, mountErr, disk.MountInfoPath(s.mountPID)))
					continue
				}
				if len(mountFailures) > 0 {
					// A malformed record can hide a matching mount. Never emit a
					// complete-looking correlation from a partial mount view.
					var details []string
					for _, failedMount := range mountFailures {
						details = append(details, failedMount.Err.Error())
					}
					f := &observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, Detail: strings.Join(details, "; "), Operation: disk.MountInfoPath(s.mountPID)}
					out = append(out, Reading{Signal: signal, Target: target, Failure: f})
					continue
				}
				if len(matches) == 0 {
					out = append(out, Reading{Signal: signal, Target: target, Value: disk.Text("")})
					continue
				}
				var values []string
				for _, mount := range matches {
					identity := mount.Namespace + "/" + strconv.FormatUint(mount.ID, 10)
					value := strconv.Quote(identity)
					if signal == signals.FSTypes {
						value += "=" + strconv.Quote(mount.FSType)
					} else if signal == signals.Sources {
						value += "=" + strconv.Quote(mount.Source)
					}
					values = append(values, value)
				}
				out = append(out, Reading{Signal: signal, Target: target, Value: disk.Text(strings.Join(values, "\n"))})
				continue
			}
			field := queuePaths[signal]
			name := path.Join("sys/fs/fuse/connections", entry.Name(), field)
			raw, err := fs.ReadFile(s.files, name)
			if err != nil {
				out = append(out, failure(signal, target, err, name))
				continue
			}
			value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
			if err != nil {
				out = append(out, failure(signal, target, err, name))
				continue
			}
			out = append(out, Reading{Signal: signal, Target: target, Value: disk.Uint(value)})
		}
	}
	if count == 0 && len(out) == 0 {
		for _, signal := range selected {
			out = append(out, absent(signal))
		}
	}
	return out, nil
}

func connectionID(name string) (uint64, error) {
	for _, digit := range name {
		if digit < '0' || digit > '9' {
			return 0, &strconv.NumError{Func: "connection ID", Num: name, Err: strconv.ErrSyntax}
		}
	}
	id, err := strconv.ParseUint(name, 10, 64)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, &strconv.NumError{Func: "connection ID", Num: name, Err: fmt.Errorf("must be positive")}
	}
	return id, nil
}
