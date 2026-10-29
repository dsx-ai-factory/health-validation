// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package rdmatopo collects ACS and IOMMU group findings from rdma_topo check.
package rdmatopo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/rdmatopo"
	"github.com/dsx-ai-factory/health-validation/collect/source/rdmatopo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ID identifies the rdma_topo sensor and observation source.
const ID = "pcie.rdma_topo"

type sensor struct {
	reader   *rdmatopo.Reader
	sequence atomic.Uint64
	runMu    sync.Mutex
}

// New uses rdma_topo on PATH when reader is nil. Requires permission to read
// PCI configuration and topology information. Confirmed findings are retained;
// collection failures are reported separately as errored observations.
func New(reader *rdmatopo.Reader) collect.Sensor {
	if reader == nil {
		reader = rdmatopo.New(nil)
	}
	return &sensor{reader: reader}
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return signals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	return s.MeasureWithPartial(ctx, nil, opts...)
}

// MeasureWithPartial publishes independently owned snapshots for this call.
// The callback must not block; it may run after MeasureWithPartial returns.
func (s *sensor) MeasureWithPartial(ctx context.Context, publish func(*observation.ObservationBatch), opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if ctx == nil {
		return nil, errors.New("collection context is required")
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
	cycle := op.Cycle()
	if cycle == nil {
		cycle, err = collect.NewCycle(fmt.Sprintf("%s.local.%d", ID, s.sequence.Add(1)), now.AsTime())
		if err != nil {
			return nil, err
		}
	} else {
		now = timestamppb.New(cycle.StartedAt())
	}
	id := cycle.CollectionID()
	if len(selected) == 0 {
		return &observation.ObservationBatch{CollectionId: id}, nil
	}
	var partial atomic.Pointer[observation.ObservationBatch]
	batch, err := collect.Run(ctx, cycle, ID, func(ctx context.Context) (*observation.ObservationBatch, error) {
		if !s.runMu.TryLock() {
			return nil, errors.New("rdma_topo check is already running")
		}
		defer s.runMu.Unlock()
		result := s.reader.Read(ctx, func(result rdmatopo.Result) {
			current := observations(result, id, selected, now, false)
			if ctx.Err() == nil {
				partial.Store(current)
				if publish != nil {
					publish(proto.Clone(current).(*observation.ObservationBatch))
				}
			}
		})
		return observations(result, id, selected, now, result.Err == nil), nil
	})
	if err != nil {
		batch = partial.Load()
	}
	if batch == nil {
		batch = &observation.ObservationBatch{CollectionId: id}
	}
	batch = proto.Clone(batch).(*observation.ObservationBatch)
	if err != nil {
		for _, definition := range selected {
			addFailure(batch, definition, timestamppb.New(cycle.StartedAt()), err)
		}
	}
	return batch, nil
}

func observations(result rdmatopo.Result, id string, selected collect.Signals, at *timestamppb.Timestamp, complete bool) *observation.ObservationBatch {
	batch := &observation.ObservationBatch{CollectionId: id}
	hasFindings := len(result.ACSFailures)+len(result.IOMMUGroupFailures) > 0
	for _, definition := range selected {
		var value any
		switch definition.ID() {
		case signals.ACSFailures.ID():
			value = append([]rdmatopo.Finding{}, result.ACSFailures...)
		case signals.IOMMUGroupFailures.ID():
			value = append([]rdmatopo.Finding{}, result.IOMMUGroupFailures...)
		case signals.Evidence.ID():
			value = result.Evidence
		}
		if definition.ID() == signals.Evidence.ID() || complete || hasFindings {
			data, err := json.Marshal(value)
			if err != nil {
				addFailure(batch, definition, at, err)
				continue
			}
			batch.Observations = append(batch.Observations, &observation.Observation{
				SignalId: definition.ID(), Source: ID, Target: observation.NodeTarget(""), ObservedAt: at, Unit: definition.Unit(),
				Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: string(data)}}},
			})
		}
		if result.Err != nil && definition.ID() != signals.Evidence.ID() {
			addFailure(batch, definition, at, result.Err)
		}
	}
	return batch
}

func addFailure(batch *observation.ObservationBatch, definition collect.Signal, at *timestamppb.Timestamp, err error) {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, rdmatopo.ErrUnreadable):
		category = observation.CollectionErrorCategoryUnavailable
	case errors.Is(err, rdmatopo.ErrUnsupported):
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, rdmatopo.ErrMalformed):
		category = observation.CollectionErrorCategoryParseError
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	}
	for _, current := range batch.Observations {
		if current.SignalId == definition.ID() && current.GetTarget() == nil && current.GetCollectionError() != nil {
			current.GetCollectionError().Detail += "; " + err.Error()
			if current.GetCollectionError().Category == observation.CollectionErrorCategoryTimeout {
				current.GetCollectionError().Category = category
			}
			return
		}
	}
	batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(
		definition.ID(), definition.Unit(), ID, "rdma_topo check", nil, at,
		&observation.CollectionError{Category: category, Detail: err.Error()}))
}
