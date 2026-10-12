// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package host collects read-only host file and task accounting from procfs.
package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	hostsignals "github.com/dsx-ai-factory/health-validation/collect/signals/host"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ID = "host.procfs"

type sensor struct{ fsys fs.FS }

func New(fsys fs.FS) collect.Sensor {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	return &sensor{fsys: fsys}
}
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return hostsignals.All() }

type fileResult struct {
	err    error
	fields []string
	data   []byte
}

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	op := collect.NewOp(opts...)
	if op.GPUInventory() {
		return nil, fmt.Errorf("GPU inventory is not supported by %s", ID)
	}
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	if cycle := op.Cycle(); cycle != nil {
		now = timestamppb.New(cycle.StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	cache := make(map[string]fileResult)
	for _, definition := range selected {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		path, index := "proc/stat", 0
		switch definition {
		case hostsignals.FileHandlesAllocated:
			path = "proc/sys/fs/file-nr"
		case hostsignals.FileHandlesUnused:
			path, index = "proc/sys/fs/file-nr", 1
		case hostsignals.FileHandlesMax:
			path = "proc/sys/fs/file-max"
		case hostsignals.OpenFileDescriptors:
			path = "proc/*/fd"
		}
		var value uint64
		var sourceErr error
		cat := observation.CollectionErrorCategoryParseError
		if definition == hostsignals.OpenFileDescriptors {
			value, sourceErr = s.descriptorCount(ctx)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if sourceErr != nil {
				cat = readCategory(sourceErr)
			}
			if errors.Is(sourceErr, errNoProcesses) {
				batch.Observations = append(batch.Observations, observation.NewNoValueObservation(
					definition.ID(), definition.Unit(), ID, nil, now,
					&observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "no visible process descriptor tables"},
				))
				continue
			}
		} else {
			result, ok := cache[path]
			if !ok {
				var data []byte
				data, result.err = fs.ReadFile(s.fsys, path)
				result.data = data
				if result.err == nil {
					result.fields = strings.Fields(string(data))
				}
				cache[path] = result
			}
			if result.err != nil {
				sourceErr, cat = result.err, readCategory(result.err)
			} else if path == "proc/stat" {
				key := map[string]string{hostsignals.ProcessesCreated.ID(): "processes", hostsignals.RunnableTasks.ID(): "procs_running", hostsignals.BlockedTasks.ID(): "procs_blocked"}[definition.ID()]
				found := false
				for _, line := range strings.Split(string(result.data), "\n") {
					fields := strings.Fields(line)
					if len(fields) == 0 || fields[0] != key {
						continue
					}
					if found {
						sourceErr = fmt.Errorf("duplicate %s", key)
						break
					}
					found = true
					if len(fields) != 2 {
						sourceErr = fmt.Errorf("invalid %s record", key)
						break
					}
					value, sourceErr = strconv.ParseUint(fields[1], 10, 64)
				}
				if !found {
					sourceErr = fmt.Errorf("kernel does not expose %s", key)
					cat = observation.CollectionErrorCategoryUnsupported
				}
			} else {
				expected := 1
				if path == "proc/sys/fs/file-nr" {
					expected = 3
				}
				if len(result.fields) != expected {
					sourceErr = fmt.Errorf("expected %d fields", expected)
				} else {
					value, sourceErr = strconv.ParseUint(result.fields[index], 10, 64)
				}
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if sourceErr != nil {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(definition.ID(), definition.Unit(), ID,
				"read/parse /"+path, observation.NodeTarget(""), now, &observation.CollectionError{Category: cat, Detail: sourceErr.Error()}))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId: definition.ID(), Source: ID,
			Target: observation.NodeTarget(""), ObservedAt: now, Unit: definition.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}},
		})
	}
	return batch, nil
}

var errNoProcesses = errors.New("no visible process descriptor tables")

// A disappearing PID is normal during enumeration. Permission errors must not
// become a successful partial total, because that total would undercount.
func (s *sensor) descriptorCount(ctx context.Context) (uint64, error) {
	processes, err := fs.ReadDir(s.fsys, "proc")
	if err != nil {
		return 0, err
	}
	var total uint64
	visible := 0
	for _, process := range processes {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !process.IsDir() {
			continue
		}
		pid, err := strconv.ParseUint(process.Name(), 10, 32)
		if err != nil || pid == 0 {
			continue
		}
		files, err := fs.ReadDir(s.fsys, "proc/"+process.Name()+"/fd")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		visible++
		for _, file := range files {
			if _, err := strconv.ParseUint(file.Name(), 10, 64); err != nil {
				return 0, fmt.Errorf("invalid descriptor entry %q", file.Name())
			}
		}
		count := uint64(len(files))
		if count > math.MaxUint64-total {
			return 0, fmt.Errorf("descriptor accounting overflow")
		}
		total += count
	}
	if visible == 0 {
		return 0, errNoProcesses
	}
	return total, nil
}

func readCategory(err error) observation.CollectionErrorCategory {
	if errors.Is(err, fs.ErrPermission) {
		return observation.CollectionErrorCategoryPermissionDenied
	}
	if errors.Is(err, fs.ErrNotExist) {
		return observation.CollectionErrorCategoryUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return observation.CollectionErrorCategoryTimeout
	}
	return observation.CollectionErrorCategorySourceError
}
