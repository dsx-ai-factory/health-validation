// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmsg selects kernel records by regular expression for consumer-specific interpretation.
package kmsg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/kernel"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

const (
	ID     = "kernel.kmsg"
	source = "kmsg"
)

var errInUse = errors.New("kmsg read already in progress")

// Pattern assigns a stable signal ID to a regular expression. A match emits the
// exact expression as a string value on the original kernel-record entity.
type Pattern struct {
	SignalID string
	Regex    string
}

type sensor struct {
	read     func(context.Context) ([]kmsgsource.Message, error)
	patterns map[string]*regexp.Regexp
	signals  collect.Signals
	mu       sync.Mutex
}

// DefaultPatterns selects generic kernel faults without assigning health severity.
func DefaultPatterns() []Pattern {
	return []Pattern{
		{SignalID: kernel.Panic.ID(), Regex: `Kernel [Pp]anic`},
		{SignalID: kernel.OOM.ID(), Regex: `invoked oom-killer:|oom-kill:|(?:Out of memory|Memory cgroup out of memory): Killed process`},
		{SignalID: kernel.HungTask.ID(), Regex: `task [^:]+:\d+ .*blocked for more than \d+ seconds`},
		{SignalID: kernel.Lockup.ID(), Regex: `(?:soft lockup - CPU#\d+ stuck|[Hh]ard LOCKUP|hard lockup)`},
		{SignalID: kernel.FileMax.ID(), Regex: `VFS: file-max limit \d+ reached`},
	}
}

// New uses ReadAll when read is nil and DefaultPatterns when patterns is omitted.
// Custom patterns replace the defaults. Signal IDs must be unique and cannot
// reuse kernel.message or kernel.priority. Empty or invalid patterns are rejected.
// Reader lifetime remains with the caller.
func New(read func(context.Context) ([]kmsgsource.Message, error), patterns ...Pattern) (collect.Sensor, error) {
	if len(patterns) == 0 {
		patterns = DefaultPatterns()
	}
	signals := collect.Signals{kernel.Message, kernel.Priority}
	compiled := make(map[string]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		if strings.TrimSpace(pattern.SignalID) == "" || strings.TrimSpace(pattern.SignalID) != pattern.SignalID {
			return nil, fmt.Errorf("kmsg pattern %d requires a nonempty signal ID without surrounding whitespace", i)
		}
		if pattern.SignalID == kernel.Message.ID() || pattern.SignalID == kernel.Priority.ID() || compiled[pattern.SignalID] != nil {
			return nil, fmt.Errorf("kmsg pattern %d repeats signal ID %q", i, pattern.SignalID)
		}
		if strings.TrimSpace(pattern.Regex) == "" {
			return nil, fmt.Errorf("kmsg pattern %d is empty", i)
		}
		re, err := regexp.Compile(pattern.Regex)
		if err != nil {
			return nil, fmt.Errorf("compile kmsg pattern %d: %w", i, err)
		}
		compiled[pattern.SignalID] = re
		signals = append(signals, collect.NewSignal(pattern.SignalID, ""))
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
	if len(selected) == 0 {
		return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}, nil
	}
	cycle := op.Cycle()
	if cycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	now := timestamppb.New(cycle.StartedAt())
	messages, err := collect.Run(ctx, cycle, source, func(ctx context.Context) ([]kmsgsource.Message, error) {
		if !s.mu.TryLock() {
			return nil, errInUse
		}
		defer s.mu.Unlock()
		return s.read(ctx)
	})
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
		appendErrors(batch, selected, now, category, err.Error())
	}
	for _, message := range messages {
		// Injected readers must provide the same boot-scoped identity as ReadAll.
		if strings.TrimSpace(message.BootID) == "" || message.Timestamp.IsZero() || timestamppb.New(message.Timestamp).CheckValid() != nil {
			appendErrors(batch, selected, now, observation.CollectionErrorCategoryParseError, "kernel record requires a boot ID and valid timestamp")
			continue
		}
		matches := make(map[string]string)
		for id, pattern := range s.patterns {
			if pattern.MatchString(message.Body) {
				matches[id] = pattern.String()
			}
		}
		if len(matches) == 0 {
			continue
		}
		entity := &observation.Entity{Type: "kernel_record", Id: message.BootID + "/" + strconv.Itoa(message.Sequence)}
		for _, signal := range selected {
			value := &observation.Value{Kind: &observation.Value_StringValue{StringValue: message.Body}}
			switch signal.ID() {
			case kernel.Message.ID():
			case kernel.Priority.ID():
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
				Entity:     entity,
				ObservedAt: timestamppb.New(message.Timestamp),
				Outcome:    &observation.Observation_Value{Value: value},
			})
		}
	}
	return batch, nil
}

func appendErrors(batch *observation.ObservationBatch, selected collect.Signals, now *timestamppb.Timestamp, category observation.CollectionErrorCategory, detail string) {
	for _, signal := range selected {
		batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(
			signal.ID(), signal.Unit(), source, "read", nil, now,
			&observation.CollectionError{Category: category, Detail: detail},
		))
	}
}
