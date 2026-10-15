// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package cpu collects host CPU topology and cumulative activity.
package cpu

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ID identifies the sysfs CPU sensor; it is also the observation source.
const ID = "cpu.sysfs"

// paths are relative to the filesystem root. cpu/possible lists every CPU the
// kernel could bring online, including hotplug slots; cpu/online lists those
// currently online. The kernel creates a node directory only for an online
// NUMA node, so node/online matches what topology tools report.
var paths = map[string]string{
	cpusignals.PossibleCount.ID():       "sys/devices/system/cpu/possible",
	cpusignals.OnlineCount.ID():         "sys/devices/system/cpu/online",
	cpusignals.NUMANodeOnlineCount.ID(): "sys/devices/system/node/online",
}

type sensor struct {
	fsys fs.FS
}

// New returns the CPU sensor reading sysfs from fsys, or from the host root
// when fsys is nil.
func New(fsys fs.FS) collect.Sensor {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	return &sensor{fsys: fsys}
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return cpusignals.All() }

// Measure reads selected topology and activity files independently.
// Cancellation fails the call instead of returning a partial snapshot.
//
// The cycle bounds the wait for both topology and activity acquisition.
// Observations target the node with an empty ID: collection is node-local,
// so the consumer supplies node identity.
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
		return nil, fmt.Errorf("select CPU signals: %w", err)
	}
	now := timestamppb.Now()
	if cycle := op.Cycle(); cycle != nil {
		now = timestamppb.New(cycle.StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("collect CPU: %w", err)
	}
	if len(selected) == 0 {
		return batch, nil
	}
	cycle := op.Cycle()
	if cycle == nil {
		cycle, err = collect.NewCycle("cpu.local", now.AsTime())
		if err != nil {
			return nil, err
		}
	}
	result, err := collect.Run(ctx, cycle, ID, func(ctx context.Context) (*observation.ObservationBatch, error) {
		return s.read(ctx, selected, now, batch.CollectionId)
	})
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("collect CPU: %w", err)
	}
	if err != nil {
		for _, definition := range selected {
			batch.Observations = append(batch.Observations, failure(definition, now, "read", readCategory(err), err))
		}
		return batch, nil
	}
	return result, nil
}

func (s *sensor) read(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp, collectionID string) (*observation.ObservationBatch, error) {
	// This batch belongs to the acquisition callback, which may outlive Run.
	batch := &observation.ObservationBatch{CollectionId: collectionID}
	activity := s.readActivity(ctx, selected, now)
	for _, definition := range selected {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("collect CPU: %w", err)
		}
		path := paths[definition.ID()]
		if activityObservation, ok := activity[definition.ID()]; ok {
			batch.Observations = append(batch.Observations, activityObservation)
			continue
		}
		data, err := fs.ReadFile(s.fsys, path)
		if err != nil {
			batch.Observations = append(batch.Observations, failure(definition, now, "read /"+path, readCategory(err), err))
			continue
		}
		count, err := parseListCount(data)
		if err != nil {
			batch.Observations = append(batch.Observations, failure(definition, now, "parse /"+path,
				observation.CollectionErrorCategoryParseError, err))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   definition.ID(),
			Source:     ID,
			Target:     observation.NodeTarget(""),
			ObservedAt: now,
			Unit:       definition.Unit(),
			Outcome:    &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: count}}},
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("collect CPU: %w", err)
	}
	return batch, nil
}

func readCategory(err error) observation.CollectionErrorCategory {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		return observation.CollectionErrorCategoryUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		return observation.CollectionErrorCategoryTimeout
	default:
		return observation.CollectionErrorCategorySourceError
	}
}

func failure(definition collect.Signal, now *timestamppb.Timestamp, operation string, category observation.CollectionErrorCategory, err error) *observation.Observation {
	return observation.NewCollectionErrorObservation(definition.ID(), definition.Unit(), ID, operation,
		observation.NodeTarget(""), now, &observation.CollectionError{Category: category, Detail: err.Error()})
}
