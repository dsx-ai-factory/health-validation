// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"io/fs"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	kmsgsignals "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg"
	kmsgmemory "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg/memory"
	kmsgxid "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg/xid"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

func TestMeasure(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("cycle", now)
	require.NoError(t, err)
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{Timestamp: now, BootID: "boot", Body: "same", Sequence: 1, Priority: 6},
			{Timestamp: now, BootID: "boot", Body: "same", Sequence: 2, Priority: 6},
		}, nil
	})
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Equal(t, "cycle", batch.CollectionId)
	require.Len(t, batch.Observations, 6)
	require.Equal(t, "boot", batch.Observations[0].GetTarget().GetKernelRecord().GetBootId())
	require.Equal(t, int64(1), batch.Observations[0].GetTarget().GetKernelRecord().GetSequence())
	require.Equal(t, "boot", batch.Observations[3].GetTarget().GetKernelRecord().GetBootId())
	require.Equal(t, int64(2), batch.Observations[3].GetTarget().GetKernelRecord().GetSequence())
	require.Equal(t, int64(6), batch.Observations[1].GetValue().GetIntValue())
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(kmsgsignals.Message.ID()))
	require.NoError(t, err)
	require.Len(t, batch.GetObservations(), 2)
	_, err = s.Measure(context.Background())
	require.Error(t, err)
	_, err = s.Measure(context.Background(), collect.WithSignalID("unknown"))
	require.Error(t, err)
	s = newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) { return nil, fs.ErrPermission })
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[0].GetCollectionError().GetCategory())
}

func TestTimeoutDoesNotStartAnotherRead(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) { <-release; return nil, nil })
	cycle, err := collect.NewCycleWithTimeout("first", time.Now(), 10*time.Millisecond)
	require.NoError(t, err)
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[0].GetCollectionError().GetCategory())
	cycle, err = collect.NewCycle("second", time.Now())
	require.NoError(t, err)
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
}

func TestPartialAndMalformedRecords(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("partial", now)
	require.NoError(t, err)
	s := newRawSensor(t, func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{BootID: "boot", Timestamp: now, Sequence: 1, Body: "valid"},
			{Timestamp: now, Sequence: 2, Body: "missing boot ID"},
		}, kmsgsource.ErrMalformedRecord
	})
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(kmsgsignals.Message.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 3)
	require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[0].GetCollectionError().GetCategory())
	require.Equal(t, "valid", batch.Observations[1].GetValue().GetStringValue())
	require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[2].GetCollectionError().GetCategory())
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID())
	require.NoError(t, err)
	require.Empty(t, batch.Observations)
	require.Equal(t, "partial", batch.CollectionId)
}

func newRawSensor(t *testing.T, read func(context.Context) ([]kmsgsource.Message, error)) collect.Sensor {
	t.Helper()
	s, err := New(read, map[string]string{"kernel.all": `(?s).*`})
	require.NoError(t, err)
	return s
}

func TestPatterns(t *testing.T) {
	now := time.Now()
	cycle, err := collect.NewCycle("patterns", now)
	require.NoError(t, err)
	read := func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{
			{BootID: "boot", Timestamp: now, Sequence: 1, Body: "custom\nFAULT", Priority: 3},
			{BootID: "boot", Timestamp: now, Sequence: 2, Body: "custom\nFAULT", Priority: 3},
			{BootID: "boot", Timestamp: now, Sequence: 3, Body: "Kernel panic - not syncing"},
		}, fs.ErrPermission
	}
	patterns := map[string]string{"driver.failure": `(?s)custom.*FAULT`, "driver.fault": `FAULT`}
	s, err := New(read, patterns)
	require.NoError(t, err)
	patterns["driver.failure"] = "mutated"
	declared := s.Signals()
	declared[0] = collect.NewSignal("mutated", "")
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Len(t, batch.Observations, 12)
	for i := range 4 {
		require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[i].GetCollectionError().GetCategory())
	}
	for _, offset := range []int{4, 8} {
		msg, priority, first, second := batch.Observations[offset], batch.Observations[offset+1], batch.Observations[offset+2], batch.Observations[offset+3]
		require.Equal(t, kmsgsignals.Message.ID(), msg.SignalId)
		require.Equal(t, "custom\nFAULT", msg.GetValue().GetStringValue())
		require.Equal(t, int64(3), priority.GetValue().GetIntValue())
		require.Equal(t, "driver.failure", first.SignalId)
		require.Equal(t, `(?s)custom.*FAULT`, first.GetValue().GetStringValue())
		require.Equal(t, "driver.fault", second.SignalId)
		require.Equal(t, `FAULT`, second.GetValue().GetStringValue())
		for _, obs := range []*observation.Observation{msg, priority, first, second} {
			require.Equal(t, "boot", obs.GetTarget().GetKernelRecord().GetBootId())
			require.Equal(t, int64(offset/4), obs.GetTarget().GetKernelRecord().GetSequence())
		}
		require.True(t, first.ObservedAt.AsTime().Equal(now))
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID("driver.fault"))
	require.NoError(t, err)
	require.Len(t, batch.GetObservations(), 3)
	for _, obs := range batch.Observations {
		require.Equal(t, "driver.fault", obs.SignalId)
	}
	for _, patterns := range []map[string]string{
		{"custom": ""},
		{"custom": "["},
		{"": "fault"},
		{" custom ": "fault"},
		{kmsgsignals.Message.ID(): "fault"},
		{kmsgsignals.Priority.ID(): "fault"},
	} {
		_, patternErr := New(nil, patterns)
		assert.Error(t, patternErr)
	}
}

func TestDefaultPatterns(t *testing.T) {
	now := time.Now()
	patterns := kmsgmemory.DefaultPatterns()
	maps.Copy(patterns, kmsgxid.DefaultPatterns())
	cycle, err := collect.NewCycle("defaults", now)
	require.NoError(t, err)
	for _, tt := range []struct{ body, id string }{
		{"Kernel panic - not syncing: fatal exception", "kernel.panic"},
		{"python invoked oom-killer: gfp_mask=0x0", "kernel.oom"},
		{"oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/test,task_memcg=/test,task=python,pid=42,uid=0", "kernel.oom_container"},
		{"Out of memory: Killed process 42 (python)", "kernel.oom_killed_process"},
		{"Memory cgroup out of memory: Killed process 42 (python)", "kernel.oom_killed_process"},
		{"INFO: task worker:42 blocked for more than 120 seconds.", ""},
		{"watchdog: BUG: soft lockup - CPU#18 stuck for 27s! [python:42]", ""},
		{"watchdog: Watchdog detected hard LOCKUP on cpu 0", ""},
		{"VFS: file-max limit 1000000 reached", ""},
		{"normal boot", ""},
		{"NVRM: Xid (PCI:0000:01:00): 79", ""},
		{"mlx5_core: ACCESS_REG failed", ""},
	} {
		t.Run(tt.body, func(t *testing.T) {
			s, createErr := New(func(context.Context) ([]kmsgsource.Message, error) {
				return []kmsgsource.Message{{BootID: "boot", Timestamp: now, Body: tt.body}}, nil
			}, patterns)
			require.NoError(t, createErr)
			batch, measureErr := s.Measure(context.Background(), collect.WithCycle(cycle))
			require.NoError(t, measureErr)
			if tt.id == "" {
				require.Empty(t, batch.Observations)
				return
			}
			require.Len(t, batch.Observations, 3)
			require.Equal(t, tt.id, batch.Observations[2].SignalId)
			require.Equal(t, patterns[tt.id], batch.Observations[2].GetValue().GetStringValue())
		})
	}
}

func TestExpiredContextReportsTimeout(t *testing.T) {
	readCalled := false
	s, err := New(func(context.Context) ([]kmsgsource.Message, error) {
		readCalled = true
		return nil, nil
	}, map[string]string{"match": `fault`})
	require.NoError(t, err)
	cycle, err := collect.NewCycle("expired", time.Now())
	require.NoError(t, err)
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 1))
	defer cancel()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	batch, err := s.Measure(ctx, collect.WithCycle(cycle), collect.WithSignalID("match"))
	require.NoError(t, err)
	require.False(t, readCalled)
	require.Equal(t, "expired", batch.CollectionId)
	require.Len(t, batch.Observations, 1)
	require.Equal(t, "match", batch.Observations[0].SignalId)
	require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[0].GetCollectionError().GetCategory())
}

func TestEmptySelectionRequiresCycle(t *testing.T) {
	s, err := New(nil, nil)
	require.NoError(t, err)
	_, err = s.Measure(context.Background(), collect.WithSignalID())
	require.Error(t, err)
	cycle, err := collect.NewCycle("empty", time.Now())
	require.NoError(t, err)
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID())
	require.NoError(t, err)
	require.Equal(t, "empty", batch.GetCollectionId())
	require.Empty(t, batch.GetObservations())
}

func TestGPUdPatternsPreserveMatchEvidence(t *testing.T) {
	now := time.Now()
	body := "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)"
	s, err := New(func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{{BootID: "boot", Timestamp: now, Body: body, Sequence: 42}}, nil
	}, kmsgxid.DefaultPatterns())
	require.NoError(t, err)
	require.Equal(t, "kernel.kmsg", s.ID())
	cycle, err := collect.NewCycle("gpud-patterns", now)
	require.NoError(t, err)
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Len(t, batch.Observations, 4)
	require.Equal(t, body, batch.Observations[0].GetValue().GetStringValue())
	require.Equal(t, "kernel.nvrm_xid_combined", batch.Observations[2].SignalId)
	require.Equal(t, kmsgxid.DefaultPatterns()["kernel.nvrm_xid_combined"], batch.Observations[2].GetValue().GetStringValue())
	require.Equal(t, "kernel.nvrm_xid_extended", batch.Observations[3].SignalId)
	require.Equal(t, kmsgxid.DefaultPatterns()["kernel.nvrm_xid_extended"], batch.Observations[3].GetValue().GetStringValue())
	for _, obs := range batch.Observations {
		require.Equal(t, "boot", obs.GetTarget().GetKernelRecord().GetBootId())
		require.Equal(t, int64(42), obs.GetTarget().GetKernelRecord().GetSequence())
	}
}

func TestNoPatternsDoesNotRead(t *testing.T) {
	cycle, err := collect.NewCycle("no-op", time.Now())
	require.NoError(t, err)
	for _, patterns := range []map[string]string{nil, {}} {
		reads := 0
		s, createErr := New(func(context.Context) ([]kmsgsource.Message, error) {
			reads++
			return nil, fs.ErrPermission
		}, patterns)
		require.NoError(t, createErr)
		batch, measureErr := s.Measure(context.Background(), collect.WithCycle(cycle))
		require.NoError(t, measureErr)
		require.Equal(t, "no-op", batch.GetCollectionId())
		require.Empty(t, batch.GetObservations())
		require.Zero(t, reads)
	}
}
