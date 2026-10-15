// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package memory collects read-only host memory accounting from procfs.
package memory

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
	memorysignals "github.com/dsx-ai-factory/health-validation/collect/signals/memory"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ID = "memory.procfs"

type sensor struct{ fsys fs.FS }

var memoryKeys = map[string]string{
	memorysignals.TotalBytes.ID(): "MemTotal", memorysignals.FreeBytes.ID(): "MemFree",
	memorysignals.AvailableBytes.ID(): "MemAvailable", memorysignals.VmallocTotalBytes.ID(): "VmallocTotal",
	memorysignals.VmallocUsedBytes.ID(): "VmallocUsed",
}

func New(fsys fs.FS) collect.Sensor {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	return &sensor{fsys: fsys}
}
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return memorysignals.All() }

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
	if len(selected) == 0 {
		return batch, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	cycle := op.Cycle()
	if cycle == nil {
		cycle, err = collect.NewCycle("memory.local", now.AsTime())
		if err != nil {
			return nil, err
		}
	}
	result, err := collect.Run(ctx, cycle, ID, func(ctx context.Context) (*observation.ObservationBatch, error) {
		return s.read(ctx, selected, now, batch.CollectionId)
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		for _, definition := range selected {
			batch.Observations = append(batch.Observations, failure(definition, now, "read", category(err), err))
		}
		return batch, nil
	}
	return result, nil
}

func (s *sensor) read(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp, collectionID string) (*observation.ObservationBatch, error) {
	batch := &observation.ObservationBatch{CollectionId: collectionID}
	var meminfo, vmalloc []byte
	var memErr, vmErr error
	needMem, needVM := false, false
	for _, definition := range selected {
		if definition == memorysignals.BPFJITBytes {
			needVM = true
		} else {
			needMem = true
		}
	}
	if len(selected) > 0 && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if needMem {
		meminfo, memErr = fs.ReadFile(s.fsys, "proc/meminfo")
	}
	if needVM {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		vmalloc, vmErr = fs.ReadFile(s.fsys, "proc/vmallocinfo")
	}
	fields := parseMeminfo(meminfo)
	for _, definition := range selected {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		path := "proc/meminfo"
		readErr := memErr
		if definition == memorysignals.BPFJITBytes {
			path, readErr = "proc/vmallocinfo", vmErr
		}
		if readErr != nil {
			batch.Observations = append(batch.Observations, failure(definition, now, "read /"+path, category(readErr), readErr))
			continue
		}
		var value uint64
		var parseErr error
		if definition == memorysignals.BPFJITBytes {
			value, parseErr = bpfJITBytes(vmalloc)
		} else if definition == memorysignals.UsedBytes {
			var total, available uint64
			total, parseErr = memoryField(fields, "MemTotal")
			if parseErr == nil {
				available, parseErr = memoryField(fields, "MemAvailable")
			}
			if parseErr == nil {
				if available > total {
					parseErr = fmt.Errorf("MemAvailable exceeds MemTotal")
				} else {
					value = total - available
				}
			}
		} else {
			key := memoryKeys[definition.ID()]
			value, parseErr = memoryField(fields, key)
		}
		if parseErr != nil {
			cat := observation.CollectionErrorCategoryParseError
			if errors.Is(parseErr, errMissingField) {
				cat = observation.CollectionErrorCategoryUnsupported
			}
			batch.Observations = append(batch.Observations, failure(definition, now, "parse /"+path, cat, parseErr))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId: definition.ID(), Source: ID,
			Target: observation.NodeTarget(""), ObservedAt: now, Unit: definition.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}},
		})
	}
	if len(selected) > 0 && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return batch, nil
}

var errMissingField = errors.New("memory field unavailable")

func parseMeminfo(data []byte) map[string][]string {
	out := make(map[string][]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if _, duplicate := out[key]; duplicate {
			out[key] = nil
			continue
		}
		out[key] = strings.Fields(value)
	}
	return out
}

func memoryField(fields map[string][]string, key string) (uint64, error) {
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty or malformed meminfo")
	}
	row, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", errMissingField, key)
	}
	if len(row) != 2 || row[1] != "kB" {
		return 0, fmt.Errorf("invalid %s value/unit", key)
	}
	value, err := strconv.ParseUint(row[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value > math.MaxUint64/1024 {
		return 0, fmt.Errorf("%s overflows bytes", key)
	}
	return value * 1024, nil
}

func bpfJITBytes(data []byte) (uint64, error) {
	var total uint64
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "bpf_jit_alloc_exec") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return 0, fmt.Errorf("invalid BPF JIT allocation record")
		}
		size, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		if size > math.MaxUint64-total {
			return 0, fmt.Errorf("BPF JIT accounting overflow")
		}
		total += size
	}
	return total, nil
}

func category(err error) observation.CollectionErrorCategory {
	if errors.Is(err, fs.ErrPermission) {
		return observation.CollectionErrorCategoryPermissionDenied
	}
	if errors.Is(err, fs.ErrNotExist) {
		return observation.CollectionErrorCategoryUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, collect.ErrCollectionTimeout) {
		return observation.CollectionErrorCategoryTimeout
	}
	return observation.CollectionErrorCategorySourceError
}

func failure(definition collect.Signal, now *timestamppb.Timestamp, operation string, cat observation.CollectionErrorCategory, err error) *observation.Observation {
	return observation.NewCollectionErrorObservation(definition.ID(), definition.Unit(), ID, operation,
		observation.NodeTarget(""), now, &observation.CollectionError{Category: cat, Detail: err.Error()})
}
