// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/kernel"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

func TestMeasure(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("cycle", now)
	if err != nil {
		t.Fatal(err)
	}
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{Timestamp: now, BootID: "boot", Body: "same", Sequence: 1, Priority: 6},
			{Timestamp: now, BootID: "boot", Body: "same", Sequence: 2, Priority: 6},
		}, nil
	})
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	if batch.CollectionId != "cycle" || len(batch.Observations) != 6 || batch.Observations[0].GetEntity().GetId() != "boot/1" || batch.Observations[3].GetEntity().GetId() != "boot/2" || batch.Observations[1].GetValue().GetIntValue() != 6 {
		t.Fatalf("batch: %v", batch)
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(kernel.Message.ID()))
	if err != nil || len(batch.GetObservations()) != 2 {
		t.Fatalf("selection: %v / %v", batch, err)
	}
	if _, measureErr := s.Measure(context.Background()); measureErr == nil {
		t.Fatal("accepted missing cycle")
	}
	if _, measureErr := s.Measure(context.Background(), collect.WithSignalID("unknown")); measureErr == nil {
		t.Fatal("accepted unknown signal")
	}
	s = newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) { return nil, fs.ErrPermission })
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
		t.Fatalf("permission: %v / %v", batch, err)
	}
}

func TestTimeoutDoesNotStartAnotherRead(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) { <-release; return nil, nil })
	cycle, err := collect.NewCycleWithTimeout("first", time.Now(), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("timeout: %v / %v", batch, err)
	}
	cycle, err = collect.NewCycle("second", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("overlapping read: %v / %v", batch, err)
	}
}

func TestPartialAndMalformedRecords(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("partial", now)
	if err != nil {
		t.Fatal(err)
	}
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{BootID: "boot", Timestamp: now, Sequence: 1, Body: "valid"},
			{Timestamp: now, Sequence: 2, Body: "missing boot ID"},
		}, kmsgsource.ErrMalformedRecord
	})
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(kernel.Message.ID()))
	if err != nil || len(batch.Observations) != 3 {
		t.Fatalf("partial: %v / %v", batch, err)
	}
	if batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError || batch.Observations[1].GetValue().GetStringValue() != "valid" || batch.Observations[2].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
		t.Fatalf("partial outcomes: %v", batch)
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID())
	if err != nil || len(batch.Observations) != 0 || batch.CollectionId != "partial" {
		t.Fatalf("empty batch identity: %v / %v", batch, err)
	}
}

func newRawSensor(t *testing.T, read func(context.Context) ([]kmsgsource.Message, error)) collect.Sensor {
	t.Helper()
	s, err := New(read, Pattern{SignalID: "kernel.all", Regex: `(?s).*`})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPatterns(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("patterns", now)
	if err != nil {
		t.Fatal(err)
	}
	read := func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{BootID: "boot", Timestamp: now, Sequence: 1, Body: "custom\nFAULT", Priority: 3},
			{BootID: "boot", Timestamp: now, Sequence: 2, Body: "custom\nFAULT", Priority: 3},
			{BootID: "boot", Timestamp: now, Sequence: 3, Body: "Kernel panic - not syncing"},
		}, fs.ErrPermission
	}
	patterns := []Pattern{{SignalID: "driver.failure", Regex: `(?s)custom.*FAULT`}, {SignalID: "driver.fault", Regex: `FAULT`}}
	s, err := New(read, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	patterns[0].Regex = "mutated"
	declared := s.Signals()
	declared[0] = kernel.Panic
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	if len(batch.Observations) != 12 {
		t.Fatalf("batch: %v", batch)
	}
	for i := range 4 {
		if batch.Observations[i].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
			t.Fatalf("lost partial-read error: %v", batch)
		}
	}
	for _, offset := range []int{4, 8} {
		msg, priority, first, second := batch.Observations[offset], batch.Observations[offset+1], batch.Observations[offset+2], batch.Observations[offset+3]
		if msg.SignalId != kernel.Message.ID() || msg.GetValue().GetStringValue() != "custom\nFAULT" || priority.GetValue().GetIntValue() != 3 || first.SignalId != "driver.failure" || first.GetValue().GetStringValue() != `(?s)custom.*FAULT` || second.SignalId != "driver.fault" || second.GetValue().GetStringValue() != `FAULT` || first.Entity.Id != msg.Entity.Id || !first.ObservedAt.AsTime().Equal(now) {
			t.Fatalf("match evidence: %v", batch)
		}
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID("driver.fault"))
	if err != nil || len(batch.GetObservations()) != 3 {
		t.Fatalf("selection: %v / %v", batch, err)
	}
	for _, obs := range batch.Observations {
		if obs.SignalId != "driver.fault" {
			t.Fatalf("unexpected signal: %v", obs)
		}
	}
	for _, patterns := range [][]Pattern{
		{{SignalID: "custom", Regex: ""}},
		{{SignalID: "custom", Regex: "["}},
		{{Regex: "fault"}},
		{{SignalID: " custom ", Regex: "fault"}},
		{{SignalID: kernel.Message.ID(), Regex: "fault"}},
		{{SignalID: kernel.Priority.ID(), Regex: "fault"}},
		{{SignalID: "custom", Regex: "a"}, {SignalID: "custom", Regex: "b"}},
	} {
		if _, patternErr := New(nil, patterns...); patternErr == nil {
			t.Errorf("accepted invalid patterns: %v", patterns)
		}
	}
}

func TestDefaultPatterns(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("defaults", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ body, id string }{
		{"Kernel panic - not syncing: fatal exception", kernel.Panic.ID()},
		{"python invoked oom-killer: gfp_mask=0x0", kernel.OOM.ID()},
		{"oom-kill:constraint=CONSTRAINT_MEMCG,task=python,pid=42", kernel.OOM.ID()},
		{"Out of memory: Killed process 42 (python)", kernel.OOM.ID()},
		{"Memory cgroup out of memory: Killed process 42 (python)", kernel.OOM.ID()},
		{"INFO: task worker:42 blocked for more than 120 seconds.", kernel.HungTask.ID()},
		{"watchdog: BUG: soft lockup - CPU#18 stuck for 27s!", kernel.Lockup.ID()},
		{"watchdog: Watchdog detected hard LOCKUP on cpu 0", kernel.Lockup.ID()},
		{"VFS: file-max limit 1000000 reached", kernel.FileMax.ID()},
		{"normal boot", ""},
		{"NVRM: Xid (PCI:0000:01:00): 79", ""},
		{"mlx5_core: ACCESS_REG failed", ""},
	} {
		t.Run(tt.body, func(t *testing.T) {
			s, createErr := New(func(context.Context) ([]kmsgsource.Message, error) {
				return []kmsgsource.Message{{BootID: "boot", Timestamp: now, Body: tt.body}}, nil
			})
			if createErr != nil {
				t.Fatal(createErr)
			}
			batch, measureErr := s.Measure(context.Background(), collect.WithCycle(cycle))
			if measureErr != nil {
				t.Fatal(measureErr)
			}
			if tt.id == "" {
				if len(batch.Observations) != 0 {
					t.Fatalf("unexpected match: %v", batch)
				}
				return
			}
			if len(batch.Observations) != 3 || batch.Observations[2].SignalId != tt.id {
				t.Fatalf("default match: %v", batch)
			}
			for _, pattern := range DefaultPatterns() {
				if pattern.SignalID == tt.id && batch.Observations[2].GetValue().GetStringValue() != pattern.Regex {
					t.Fatalf("regex not preserved: %v", batch)
				}
			}
		})
	}
}

func TestTimeoutIncludesMatching(t *testing.T) {
	now := time.Now()
	messages := make([]kmsgsource.Message, 20000)
	body := strings.Repeat("a", 8192)
	for i := range messages {
		messages[i] = kmsgsource.Message{BootID: "boot", Timestamp: now, Sequence: i, Body: body}
	}
	s, err := New(func(context.Context) ([]kmsgsource.Message, error) { return messages, nil }, Pattern{SignalID: "slow", Regex: `a.*z`})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := collect.NewCycleWithTimeout("matching", now, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID("slow"))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Observations) != 1 || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("matching escaped the collection deadline: %v", batch)
	}
}
