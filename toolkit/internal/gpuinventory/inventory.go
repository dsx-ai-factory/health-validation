// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package gpuinventory reads one GPU inventory per collection cycle and
// derives the node-level facts that need it, so architecture discovery and
// the node GPU count share a single enumeration.
package gpuinventory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GPUCount is the number of GPUs a successful inventory enumerated with a
// readable handle, observed on NodeTarget(""), the producing node.
var GPUCount = collect.NewSignal("node.gpu.count", "1")

// SensorID identifies the collect sensor that reports [GPUCount].
const SensorID = "node.gpu_inventory"

// handleOperation is the operation collect's NVML inventory reports for a
// slot whose device handle could not be retrieved.
const handleOperation = "DeviceGetHandleByIndex"

// Inventory reads the GPU inventory of one collect sensor at most once per
// collection cycle and serves every later request in that cycle from the
// cached snapshot.
type Inventory struct {
	open     func() (collect.Sensor, error)
	source   collect.Sensor
	cycle    *collect.Cycle
	snapshot *observation.GPUInventorySnapshot
	mu       sync.Mutex
}

// New returns an Inventory over the sensor open returns. open runs on the
// first read, bounded by that cycle's timeout, and its sensor is reused.
// The sensor must support WithGPUInventory.
func New(open func() (collect.Sensor, error)) *Inventory {
	return &Inventory{open: open}
}

// Snapshot returns cycle's GPU inventory. It never returns nil: a failed
// open, a failed Measure, or a batch without exactly one snapshot becomes a
// snapshot carrying a collection error. Callers must not modify the result.
func (i *Inventory) Snapshot(ctx context.Context, cycle *collect.Cycle) *observation.GPUInventorySnapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.cycle == cycle && i.snapshot != nil {
		return i.snapshot
	}
	i.cycle, i.snapshot = cycle, i.read(ctx, cycle)
	return i.snapshot
}

func (i *Inventory) read(ctx context.Context, cycle *collect.Cycle) *observation.GPUInventorySnapshot {
	at := timestamppb.Now()
	if cycle != nil {
		at = timestamppb.New(cycle.StartedAt())
	}
	failed := func(category observation.CollectionErrorCategory, detail string) *observation.GPUInventorySnapshot {
		return &observation.GPUInventorySnapshot{Source: SensorID, CollectedAt: at, Outcome: &observation.GPUInventorySnapshot_CollectionError{
			CollectionError: &observation.CollectionError{Source: SensorID, Operation: "GPUInventory", Category: category, Detail: detail},
		}}
	}
	if i.source == nil {
		source, err := i.openSource(ctx, cycle)
		if err != nil {
			category := observation.CollectionErrorCategoryUnavailable
			if errors.Is(err, collect.ErrCollectionTimeout) {
				category = observation.CollectionErrorCategoryTimeout
			}
			return failed(category, fmt.Sprintf("open GPU inventory source: %v", err))
		}
		i.source = source
	}
	batch, err := i.source.Measure(ctx, collect.WithCycle(cycle), collect.WithSignalID(), collect.WithGPUInventory())
	if err != nil {
		return failed(observation.CollectionErrorCategorySourceError, err.Error())
	}
	snapshots := batch.GetInventory().GetGpuSnapshots()
	if len(snapshots) != 1 {
		return failed(observation.CollectionErrorCategorySourceError, fmt.Sprintf("%s returned %d GPU inventory snapshots, want 1", i.source.ID(), len(snapshots)))
	}
	snapshot := proto.Clone(snapshots[0]).(*observation.GPUInventorySnapshot)
	if snapshot.GetCollectedAt() == nil {
		snapshot.CollectedAt = at
	}
	if snapshot.GetSource() == "" {
		snapshot.Source = i.source.ID()
	}
	return snapshot
}

// openSource runs open under cycle's timeout, so a hung library load fails
// the inventory instead of the run.
func (i *Inventory) openSource(ctx context.Context, cycle *collect.Cycle) (collect.Sensor, error) {
	open := func(context.Context) (collect.Sensor, error) {
		source, err := i.open()
		if err == nil && source == nil {
			err = errors.New("no GPU inventory source")
		}
		return source, err
	}
	if cycle == nil {
		return open(ctx)
	}
	return collect.Run(ctx, cycle, SensorID, open)
}

// Count returns how many GPUs a successful snapshot enumerated, or false when
// enumeration failed. Failed slots keep their source index in the snapshot,
// but a slot whose handle could not be retrieved is not counted, matching the
// legacy NVML backend; a GPU whose handle loaded counts even if its UUID or
// another attribute could not be read. A NO_TARGETS observation is not
// consulted: only a successful snapshot proves a count, including zero.
func Count(snapshot *observation.GPUInventorySnapshot) (int64, bool) {
	if snapshot.GetInventory() == nil {
		return 0, false
	}
	var count int64
	for _, entry := range snapshot.GetInventory().GetEntries() {
		if entry.GetCollectionError().GetOperation() != handleOperation {
			count++
		}
	}
	return count, true
}

// Sensor reports [GPUCount] from the cycle's shared snapshot.
func (i *Inventory) Sensor() collect.Sensor { return countSensor{inventory: i} }

type countSensor struct {
	inventory *Inventory
}

func (countSensor) ID() string               { return SensorID }
func (countSensor) Signals() collect.Signals { return collect.Signals{GPUCount} }

func (s countSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	cycle := op.Cycle()
	if cycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	batch.Observations = append(batch.Observations, countObservation(s.inventory.Snapshot(ctx, cycle)))
	return batch, nil
}

// countObservation turns snapshot into one GPUCount observation: the count
// on success, or the snapshot's enumeration failure, scoped to its source.
func countObservation(snapshot *observation.GPUInventorySnapshot) *observation.Observation {
	count, ok := Count(snapshot)
	if !ok {
		return observation.NewCollectionErrorObservation(GPUCount.ID(), GPUCount.Unit(), snapshot.GetSource(), "GPUInventory", nil,
			snapshot.GetCollectedAt(), snapshot.GetCollectionError())
	}
	return &observation.Observation{
		SignalId:   GPUCount.ID(),
		Source:     snapshot.GetSource(),
		Target:     observation.NodeTarget(""),
		ObservedAt: snapshot.GetCollectedAt(),
		Unit:       GPUCount.Unit(),
		Outcome:    &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: count}}},
	}
}
