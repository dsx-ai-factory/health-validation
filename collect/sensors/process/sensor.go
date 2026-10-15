// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package process collects stateless procfs process snapshots.
package process

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	processsignals "github.com/dsx-ai-factory/health-validation/collect/signals/process"
	processsource "github.com/dsx-ai-factory/health-validation/collect/source/process"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ID = "process.procfs"

type sensor struct{ reader *processsource.Reader }

// New reads paths relative to fsys, or the host root when fsys is nil.
func New(fsys fs.FS) collect.Sensor      { return &sensor{reader: processsource.New(fsys)} }
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return processsignals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if op.GPUInventory() {
		return nil, fmt.Errorf("process sensor does not provide GPU inventory")
	}
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	if op.Cycle() != nil {
		now = timestamppb.New(op.Cycle().StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cycle := op.Cycle()
	if cycle == nil {
		cycle, err = collect.NewCycle("process.local", now.AsTime())
		if err != nil {
			return nil, err
		}
	}
	result, err := collect.Run(ctx, cycle, ID, func(ctx context.Context) (*observation.ObservationBatch, error) {
		return s.read(ctx, selected, now, batch.CollectionId)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		for _, definition := range selected {
			batch.Observations = append(batch.Observations, failure(definition, nil, now, "read", err))
		}
		return batch, nil
	}
	return result, nil
}

func (s *sensor) read(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp, collectionID string) (*observation.ObservationBatch, error) {
	batch := &observation.ObservationBatch{CollectionId: collectionID}
	want := make(map[string]bool, len(selected))
	for _, definition := range selected {
		want[definition.ID()] = true
	}
	pids, err := s.reader.PIDs()
	if err != nil {
		for _, definition := range selected {
			batch.Observations = append(batch.Observations, failure(definition, nil, now, "ReadDir /proc", err))
		}
		return batch, nil
	}
	needIdentity := want[processsignals.Name.ID()] || want[processsignals.State.ID()] || want[processsignals.StartTimeTicks.ID()]
	var bootID string
	var bootErr error
	if needIdentity && len(pids) > 0 {
		bootID, bootErr = s.reader.BootID()
	}
	counts := make(map[string]uint64)
	var statErrors []string
	var statErr error
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stat, err := s.reader.Stat(pid)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			statErrors = append(statErrors, fmt.Sprintf("PID %d: %v", pid, err))
			if statErr == nil || errors.Is(err, fs.ErrPermission) {
				statErr = err
			}
			continue
		}
		counts[processsignals.Count.ID()]++
		counts[processsignals.StateCount(stat.State).ID()]++
		if !needIdentity || bootErr != nil {
			continue
		}
		target := observation.ProcessTarget(bootID, pid, stat.StartTimeTicks)
		if want[processsignals.State.ID()] {
			appendValue(batch, now, target, processsignals.State, &observation.Value{Kind: &observation.Value_StringValue{StringValue: stat.State}})
		}
		if want[processsignals.StartTimeTicks.ID()] {
			appendValue(batch, now, target, processsignals.StartTimeTicks, uintValue(stat.StartTimeTicks))
		}
		if want[processsignals.Name.ID()] {
			name, err := s.reader.Comm(pid)
			if err == nil {
				// Recheck incarnation before attaching a separate comm read to stat.
				var after processsource.Stat
				after, err = s.reader.Stat(pid)
				if err == nil && after.StartTimeTicks != stat.StartTimeTicks {
					err = fmt.Errorf("PID %d reused during comm read", pid)
				}
			}
			if err != nil {
				batch.Observations = append(batch.Observations, failure(processsignals.Name, target, now, "ReadComm", err))
			} else {
				appendValue(batch, now, target, processsignals.Name, &observation.Value{Kind: &observation.Value_StringValue{StringValue: name}})
			}
		}
	}
	for _, definition := range selected {
		isProcess := definition == processsignals.Name || definition == processsignals.State || definition == processsignals.StartTimeTicks
		if isProcess {
			if bootErr != nil {
				batch.Observations = append(batch.Observations, failure(definition, nil, now, "ReadBootID", bootErr))
			} else if statErr != nil {
				failure := failure(definition, nil, now, "ReadStat", statErr)
				failure.GetCollectionError().Detail = strings.Join(statErrors, "; ")
				batch.Observations = append(batch.Observations, failure)
			} else if counts[processsignals.Count.ID()] == 0 {
				batch.Observations = append(batch.Observations, observation.NewNoValueObservation(definition.ID(), definition.Unit(), ID, nil, now, &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "No processes observed"}))
			}
			continue
		}
		if statErr != nil {
			failure := failure(definition, observation.NodeTarget(""), now, "ReadStat", statErr)
			failure.GetCollectionError().Detail = strings.Join(statErrors, "; ")
			batch.Observations = append(batch.Observations, failure)
		} else {
			appendValue(batch, now, observation.NodeTarget(""), definition, uintValue(counts[definition.ID()]))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return batch, nil
}

func uintValue(value uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}
}

func appendValue(batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, definition collect.Signal, value *observation.Value) {
	batch.Observations = append(batch.Observations, &observation.Observation{SignalId: definition.ID(), Unit: definition.Unit(), Source: ID, Target: target, ObservedAt: now, Outcome: &observation.Observation_Value{Value: value}})
}

func failure(definition collect.Signal, target *observation.ObservationTarget, now *timestamppb.Timestamp, operation string, err error) *observation.Observation {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		category = observation.CollectionErrorCategoryUnavailable
	case errors.Is(err, processsource.ErrMalformed):
		category = observation.CollectionErrorCategoryParseError
	}
	return observation.NewCollectionErrorObservation(definition.ID(), definition.Unit(), ID, operation, target, now, &observation.CollectionError{Category: category, Detail: err.Error()})
}
