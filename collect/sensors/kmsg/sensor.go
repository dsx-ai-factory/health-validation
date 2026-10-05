// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmsg selects kernel records by regular expression for consumer-specific interpretation.
// Observations identify each record by a typed boot ID and sequence target.
package kmsg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	kmsgsignals "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

const (
	ID     = "kernel.kmsg"
	source = "kmsg"
)

var errInUse = errors.New("kmsg read already in progress")

type sensor struct {
	read     func(context.Context) ([]kmsgsource.Message, error)
	patterns map[string]*regexp.Regexp
	signals  collect.Signals
	mu       sync.Mutex
}

// New uses ReadAll when read is nil. Without patterns, collection is a no-op
// and does not read the source. Pass a signal-ID-to-regex map to opt in.
// Signal IDs must be unique and cannot reuse kernel.message or kernel.priority.
// Empty or invalid patterns are rejected. Reader lifetime remains with the caller.
func New(read func(context.Context) ([]kmsgsource.Message, error), patterns map[string]string) (collect.Sensor, error) {
	signals := collect.Signals{kmsgsignals.Message, kmsgsignals.Priority}
	compiled := make(map[string]*regexp.Regexp, len(patterns))
	ids := make([]string, 0, len(patterns))
	for id := range patterns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		expression := patterns[id]
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("kmsg pattern %q requires a nonempty signal ID without surrounding whitespace", id)
		}
		if id == kmsgsignals.Message.ID() || id == kmsgsignals.Priority.ID() {
			return nil, fmt.Errorf("kmsg pattern uses reserved signal ID %q", id)
		}
		if strings.TrimSpace(expression) == "" {
			return nil, fmt.Errorf("kmsg pattern %q is empty", id)
		}
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("compile kmsg pattern %q: %w", id, err)
		}
		compiled[id] = re
		signals = append(signals, collect.NewSignal(id, ""))
	}
	if read == nil {
		read = kmsgsource.ReadAll
	}
	return &sensor{read: read, patterns: compiled, signals: signals}, nil
}

func (*sensor) ID() string                 { return ID }
func (s *sensor) Signals() collect.Signals { return append(collect.Signals(nil), s.signals...) }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	cycle := op.Cycle()
	if cycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	if len(selected) == 0 || len(s.patterns) == 0 {
		return &observation.ObservationBatch{CollectionId: cycle.CollectionID()}, nil
	}
	now := timestamppb.New(cycle.StartedAt())
	batch, err := collect.Run(ctx, cycle, source, func(ctx context.Context) (*observation.ObservationBatch, error) {
		if !s.mu.TryLock() {
			return nil, errInUse
		}
		defer s.mu.Unlock()
		return s.measure(ctx, selected, now)
	})
	if batch == nil {
		batch = &observation.ObservationBatch{}
	}
	batch.CollectionId = cycle.CollectionID()
	if err != nil {
		category := observation.CollectionErrorCategorySourceError
		switch {
		case errors.Is(err, collect.ErrCollectionTimeout), errors.Is(err, context.DeadlineExceeded):
			category = observation.CollectionErrorCategoryTimeout
		case errors.Is(err, kmsgsource.ErrMalformedRecord):
			category = observation.CollectionErrorCategoryParseError
		case errors.Is(err, fs.ErrPermission):
			category = observation.CollectionErrorCategoryPermissionDenied
		case errors.Is(err, errors.ErrUnsupported):
			category = observation.CollectionErrorCategoryUnsupported
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrClosed), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, errInUse):
			category = observation.CollectionErrorCategoryUnavailable
		}
		failures := &observation.ObservationBatch{}
		appendErrors(failures, selected, now, category, err.Error())
		batch.Observations = append(failures.Observations, batch.Observations...)
	}
	return batch, nil
}

func (s *sensor) measure(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp) (*observation.ObservationBatch, error) {
	messages, readErr := s.read(ctx)
	batch := &observation.ObservationBatch{}
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Injected readers must provide the same boot-scoped identity as ReadAll.
		if strings.TrimSpace(message.BootID) == "" || message.Timestamp.IsZero() || timestamppb.New(message.Timestamp).CheckValid() != nil {
			appendErrors(batch, selected, now, observation.CollectionErrorCategoryParseError, "kernel record requires a boot ID and valid timestamp")
			continue
		}
		matches := make(map[string]string)
		for id, pattern := range s.patterns {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if pattern.MatchString(message.Body) {
				matches[id] = pattern.String()
			}
		}
		if len(matches) == 0 {
			continue
		}
		target := observation.KernelRecordTarget(message.BootID, message.Sequence)
		for _, signal := range selected {
			value := &observation.Value{Kind: &observation.Value_StringValue{StringValue: message.Body}}
			switch signal.ID() {
			case kmsgsignals.Message.ID():
			case kmsgsignals.Priority.ID():
				value = &observation.Value{Kind: &observation.Value_IntValue{IntValue: int64(message.Priority)}}
			default:
				pattern, matched := matches[signal.ID()]
				if !matched {
					continue
				}
				value = &observation.Value{Kind: &observation.Value_StringValue{StringValue: pattern}}
			}
			batch.Observations = append(batch.Observations, &observation.Observation{
				SignalId:   signal.ID(),
				Unit:       signal.Unit(),
				Source:     source,
				Target:     target,
				ObservedAt: timestamppb.New(message.Timestamp),
				Outcome:    &observation.Observation_Value{Value: value},
			})
		}
	}
	return batch, readErr
}

func appendErrors(batch *observation.ObservationBatch, selected collect.Signals, now *timestamppb.Timestamp, category observation.CollectionErrorCategory, detail string) {
	for _, signal := range selected {
		batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(
			signal.ID(), signal.Unit(), source, "read", nil, now,
			&observation.CollectionError{Category: category, Detail: detail},
		))
	}
}
