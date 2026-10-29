// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"fmt"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	kmsgsignals "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

// KmsgID identifies the kernel-backed Xid sensor.
const KmsgID = "xid.kmsg"

type kmsgSensor struct{ kernel collect.Sensor }

// NewKmsg collects Xid facts from read, or from kmsg.ReadAll when read is nil.
// Pass a caller-owned Stream.Read to share one native reader across consumers.
// The sensor never closes the source. Raw kernel.message and kernel.priority
// may also be selected; they include non-Xid records so the same measurement
// can feed other kernel consumers. Parsed facts retain each kernel record's
// boot ID, sequence, and original timestamp. No boot-wide scan marker is emitted:
// a kernel stream only exposes its bounded, best-effort retained window.
func NewKmsg(read func(context.Context) ([]kmsgsource.Message, error)) (collect.Sensor, error) {
	kernel, err := kmsgsensor.New(read, map[string]string{"xid.kernel_record": `(?s).*`})
	if err != nil {
		return nil, err
	}
	return &kmsgSensor{kernel: kernel}, nil
}

func (*kmsgSensor) ID() string { return KmsgID }
func (*kmsgSensor) Signals() collect.Signals {
	return append(collect.Signals{kmsgsignals.Message, kmsgsignals.Priority}, xidsignals.EventSignals()...)
}

func (s *kmsgSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	if op.Cycle() == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	want := make(map[string]bool, len(selected))
	parse := false
	for _, sig := range selected {
		want[sig.ID()] = true
		if sig.ID() != kmsgsignals.Message.ID() && sig.ID() != kmsgsignals.Priority.ID() {
			parse = true
		}
	}
	// The underlying sensor owns collection, record validation, and read-error
	// classification, including a recoverable overrun with partial records.
	raw, err := s.kernel.Measure(ctx, collect.WithCycle(op.Cycle()), collect.WithSignalID(kmsgsignals.Message.ID(), kmsgsignals.Priority.ID()))
	if err != nil {
		return nil, err
	}
	for _, reading := range raw.Observations {
		if failure := reading.GetCollectionError(); failure != nil {
			// Both raw signals carry the same read failure. Expand it once to
			// every selected field, including a facts-only selection.
			if reading.SignalId == kmsgsignals.Message.ID() {
				for _, sig := range selected {
					batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(
						sig.ID(), sig.Unit(), KmsgID, "read", reading.Target, reading.ObservedAt, failure))
				}
			}
			continue
		}
		if want[reading.SignalId] {
			batch.Observations = append(batch.Observations, reading)
		}
		if !parse || reading.SignalId != kmsgsignals.Message.ID() || !xidsignals.Match(reading.GetValue().GetStringValue()) {
			continue
		}
		event, err := xidsignals.Parse(reading.GetValue().GetStringValue())
		if err != nil {
			for _, sig := range selected {
				if sig.ID() == kmsgsignals.Message.ID() || sig.ID() == kmsgsignals.Priority.ID() {
					continue
				}
				batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(
					sig.ID(), sig.Unit(), KmsgID, "parse", reading.Target, reading.ObservedAt,
					&observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, Detail: err.Error()}))
			}
			continue
		}
		addEvent(event, func(sig collect.Signal, v *observation.Value) {
			if want[sig.ID()] {
				fact := value(sig, reading.Target, reading.ObservedAt, v)
				fact.Source = KmsgID
				batch.Observations = append(batch.Observations, fact)
			}
		})
	}
	return batch, nil
}
