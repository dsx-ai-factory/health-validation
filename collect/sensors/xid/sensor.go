// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package xid collects parsed Xid facts from the journal or kernel records.
// The journal sensor supports boot history and time bounds. The kmsg sensor
// supports a caller-owned stream, whose retained window can evict old records.
package xid

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ID identifies the journal Xid sensor; it is also the observation source.
const ID = "xid.journal"

var errInUse = errors.New("xid journal read already in progress")

type sensor struct {
	reader *journal.Reader
	ranges timeRanges
	mu     sync.Mutex
}

// New returns the Xid sensor reading through reader, or through journalctl on
// PATH when reader is nil.
func New(reader *journal.Reader) collect.Sensor {
	if reader == nil {
		reader = journal.New(nil)
	}
	return &sensor{reader: reader}
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return xidsignals.All() }

// Measure emits each Xid entry's fields on its journal-entry target, a parse
// error on the target of an entry that does not parse, and the scan marker
// once the read succeeds. A failed read yields one untargeted failure per
// selected signal and no marker, so it cannot pass as a clean scan.
func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, fmt.Errorf("select Xid signals: %w", err)
	}
	// journald stamps entries with its own clock when it writes them, so an
	// entry written before a backward clock step reads as in the future.
	now := timestamppb.Now()
	if cycle := op.Cycle(); cycle != nil {
		now = timestamppb.New(cycle.StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	if !s.mu.TryLock() {
		batch.Observations = scanFailure(selected, now, errInUse)
		return batch, nil
	}
	defer s.mu.Unlock()

	scan := &scan{want: map[string]bool{}, at: now, ranges: s.ranges}
	for _, definition := range selected {
		scan.want[definition.ID()] = true
	}
	var readErr error
	if len(s.ranges.gpuFrom) == 0 {
		// The reader's inclusive bounds reduce output; scan keeps the exclusive
		// bounds. GPU overrides require boot history for unconfigured GPUs.
		readErr = s.reader.ReadKernelBootRange(ctx, s.ranges.from, s.ranges.to, scan.entry)
	} else {
		readErr = s.reader.ReadKernelBoot(ctx, scan.entry)
	}
	if readErr != nil {
		batch.Observations = scanFailure(selected, now, readErr)
		return batch, nil
	}
	batch.Observations = scan.observations
	if scan.want[xidsignals.ScanWindow.ID()] {
		batch.Observations = append(batch.Observations,
			value(xidsignals.ScanWindow, observation.NodeTarget(""), now, stringValue(s.ranges.description())))
	}
	return batch, nil
}

type scan struct {
	ranges       timeRanges
	want         map[string]bool
	at           *timestamppb.Timestamp
	observations []*observation.Observation
	entries      int
}

func (s *scan) entry(e journal.Entry) error {
	s.entries++
	if !xidsignals.Match(e.Message) {
		return nil
	}
	if s.ranges.bounded() {
		if e.Realtime.IsZero() {
			return fmt.Errorf("Xid entry %d has no realtime timestamp: %w", s.entries, journal.ErrMalformedEntry)
		}
		if s.ranges.excludes(e.Realtime, e.Message) {
			return nil
		}
	}
	if e.BootID == "" || e.Cursor == "" {
		return fmt.Errorf("journal entry %d has no boot ID or cursor: %w", s.entries, journal.ErrMalformedEntry)
	}
	target := func() *observation.ObservationTarget { return observation.JournalEntryTarget(e.BootID, e.Cursor) }
	event, err := xidsignals.Parse(e.Message)
	if err != nil {
		return s.parseFailure(target(), err)
	}
	add := func(definition collect.Signal, v *observation.Value) {
		if s.want[definition.ID()] {
			s.observations = append(s.observations, value(definition, target(), s.at, v))
		}
	}
	addEvent(event, add)
	return nil
}

// addEvent emits the same parsed facts for journal and kernel sources.
func addEvent(event xidsignals.Event, add func(collect.Signal, *observation.Value)) {
	add(xidsignals.Code, intValue(event.Code))
	add(xidsignals.PCIAddress, stringValue(event.PCIAddress))
	if event.HasPID {
		add(xidsignals.PID, intValue(event.PID))
	}
	if event.ProcessName != "" {
		add(xidsignals.ProcessName, stringValue(event.ProcessName))
	}
	if event.GPC != nil {
		add(xidsignals.GPC, intValue(*event.GPC))
	}
	if event.TPC != nil {
		add(xidsignals.TPC, intValue(*event.TPC))
	}
	if event.SM != nil {
		add(xidsignals.SM, intValue(*event.SM))
	}
	if n := event.NVLink5; n != nil {
		add(xidsignals.NVLink5SubComponent, stringValue(n.SubComponent))
		add(xidsignals.NVLink5Fatal, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: n.Fatal}})
		add(xidsignals.NVLink5Link, intValue(n.Link))
		if n.IntrInfo != "" {
			add(xidsignals.NVLink5IntrInfo, stringValue(n.IntrInfo))
		}
		if n.ErrorStatus != "" {
			add(xidsignals.NVLink5ErrorStatus, stringValue(n.ErrorStatus))
		}
		if n.DebugData != "" {
			add(xidsignals.NVLink5DebugData, stringValue(n.DebugData))
		}
	}
	if r := event.RecoveryAction; r != nil {
		add(xidsignals.RecoveryActionFromCode, stringValue(r.FromCode))
		add(xidsignals.RecoveryActionFromName, stringValue(r.FromName))
		add(xidsignals.RecoveryActionToCode, stringValue(r.ToCode))
		add(xidsignals.RecoveryActionToName, stringValue(r.ToName))
	}
}

// parseFailure records a parse error on the entry's code. When the code is
// not selected there is no signal to carry it, so the scan fails instead.
func (s *scan) parseFailure(target *observation.ObservationTarget, err error) error {
	var parseErr *xidsignals.ParseError
	if !errors.As(err, &parseErr) || !s.want[xidsignals.Code.ID()] {
		return fmt.Errorf("entry %d: %w", s.entries, err)
	}
	s.observations = append(s.observations, observation.NewCollectionErrorObservation(
		xidsignals.Code.ID(), xidsignals.Code.Unit(), ID, "parse", target, s.at, &observation.CollectionError{
			Category:    observation.CollectionErrorCategoryParseError,
			SourceError: parseErr.Reason,
			Detail:      fmt.Sprintf("entry %d: %s", s.entries, parseErr.Detail),
		}))
	return nil
}

func scanFailure(selected collect.Signals, now *timestamppb.Timestamp, err error) []*observation.Observation {
	out := make([]*observation.Observation, 0, len(selected))
	for _, definition := range selected {
		out = append(out, observation.NewCollectionErrorObservation(
			definition.ID(), definition.Unit(), ID, "ReadKernelBoot", nil, now, &observation.CollectionError{
				Category: category(err),
				Detail:   err.Error(),
			}))
	}
	return out
}

func category(err error) observation.CollectionErrorCategory {
	var parseErr *xidsignals.ParseError
	switch {
	case errors.Is(err, collect.ErrCollectionTimeout), errors.Is(err, context.DeadlineExceeded):
		return observation.CollectionErrorCategoryTimeout
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, journal.ErrNoEntries), errors.Is(err, context.Canceled),
		errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, errInUse):
		return observation.CollectionErrorCategoryUnavailable
	case errors.Is(err, fs.ErrPermission):
		return observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, journal.ErrMalformedEntry), errors.As(err, &parseErr):
		return observation.CollectionErrorCategoryParseError
	default:
		return observation.CollectionErrorCategorySourceError
	}
}

func value(definition collect.Signal, target *observation.ObservationTarget, at *timestamppb.Timestamp, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId:   definition.ID(),
		Source:     ID,
		Target:     target,
		ObservedAt: at,
		Unit:       definition.Unit(),
		Outcome:    &observation.Observation_Value{Value: v},
	}
}

func intValue(v int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}
}

func stringValue(v string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: v}}
}
