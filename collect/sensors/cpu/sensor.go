// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package cpu is the sysfs-backed collect sensor for host CPU topology.
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

// paths are relative to the filesystem root. The kernel creates a node
// directory only for an online NUMA node, so node/online matches what
// topology tools report.
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

// Measure reads each selected list independently, so one unreadable or
// malformed file fails only its own signal. Cancellation fails the whole
// call: a partial read under a dying context proves nothing.
//
// Observations target the node with an empty ID: collection is node-local,
// so the consumer supplies node identity.
func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, fmt.Errorf("select CPU signals: %w", err)
	}
	now := timestamppb.Now()
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	for _, definition := range selected {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("collect CPU topology: %w", err)
		}
		path := paths[definition.ID()]
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
		return nil, fmt.Errorf("collect CPU topology: %w", err)
	}
	return batch, nil
}

func readCategory(err error) observation.CollectionErrorCategory {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		return observation.CollectionErrorCategoryUnavailable
	default:
		return observation.CollectionErrorCategorySourceError
	}
}

func failure(definition collect.Signal, now *timestamppb.Timestamp, operation string, category observation.CollectionErrorCategory, err error) *observation.Observation {
	return observation.NewCollectionErrorObservation(definition.ID(), definition.Unit(), ID, operation,
		observation.NodeTarget(""), now, &observation.CollectionError{Category: category, Detail: err.Error()})
}
