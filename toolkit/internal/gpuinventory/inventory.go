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

// handleOperation is the operation an NVML inventory reports for a slot
// whose device handle could not be retrieved.
const handleOperation = "DeviceGetHandleByIndex"

// Reader reads the basic GPU inventory the node needs before its catalog is
// built: enumeration, each slot's identity, and enough GPU models to name
// the architecture. Optional enrichment does not belong here, so a hung
// attribute read cannot block discovery or every other check.
type Reader interface {
	// ID keys the reader's timeout in the collection cycle.
	ID() string
	// Read returns one snapshot; a failed enumeration is a snapshot carrying
	// a collection error, and an error means the read itself failed.
	Read(ctx context.Context) (*observation.GPUInventorySnapshot, error)
}

// Inventory reads one basic GPU inventory per collection cycle and serves
// every later request in that cycle from the cached snapshot.
type Inventory struct {
	open     func() (Reader, error)
	reader   Reader
	cycle    *collect.Cycle
	snapshot *observation.GPUInventorySnapshot
	mu       sync.Mutex
}

// New returns an Inventory over the reader open returns. open runs on the
// first read, bounded by that cycle's timeout, and its reader is reused.
func New(open func() (Reader, error)) *Inventory {
	return &Inventory{open: open}
}

// Snapshot returns cycle's GPU inventory. It never returns nil: a failed
// open or read becomes a snapshot carrying a collection error. Callers must
// not modify the result.
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
	failed := func(operation string, err error) *observation.GPUInventorySnapshot {
		category := observation.CollectionErrorCategoryUnavailable
		if errors.Is(err, collect.ErrCollectionTimeout) {
			category = observation.CollectionErrorCategoryTimeout
		}
		return &observation.GPUInventorySnapshot{Source: SensorID, CollectedAt: at, Outcome: &observation.GPUInventorySnapshot_CollectionError{
			CollectionError: &observation.CollectionError{Source: SensorID, Operation: operation, Category: category, Detail: err.Error()},
		}}
	}
	if i.reader == nil {
		reader, err := run(ctx, cycle, SensorID, func(context.Context) (Reader, error) {
			reader, err := i.open()
			if err == nil && reader == nil {
				err = errors.New("no GPU inventory reader")
			}
			return reader, err
		})
		if err != nil {
			return failed("Open", fmt.Errorf("open GPU inventory source: %w", err))
		}
		i.reader = reader
	}
	snapshot, err := run(ctx, cycle, i.reader.ID(), i.reader.Read)
	if err == nil && snapshot == nil {
		err = fmt.Errorf("%s returned no GPU inventory", i.reader.ID())
	}
	if err != nil {
		return failed("GPUInventory", err)
	}
	snapshot = proto.Clone(snapshot).(*observation.GPUInventorySnapshot)
	if snapshot.GetCollectedAt() == nil {
		snapshot.CollectedAt = at
	}
	if snapshot.GetSource() == "" {
		snapshot.Source = i.reader.ID()
	}
	return snapshot
}

// run bounds call by cycle's timeout, so a hung library load or native read
// fails the inventory instead of the run.
func run[T any](ctx context.Context, cycle *collect.Cycle, source string, call func(context.Context) (T, error)) (T, error) {
	if cycle == nil {
		return call(ctx)
	}
	return collect.Run(ctx, cycle, source, call)
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
