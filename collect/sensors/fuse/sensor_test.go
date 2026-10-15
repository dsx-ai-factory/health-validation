// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fuse

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/fuse"
)

func TestInvalidRequestRejectedBeforeCollection(t *testing.T) {
	s := New(fstest.MapFS{})
	var nilCtx context.Context
	for _, opts := range [][]collect.OpOption{nil, {collect.WithSignalID()}} {
		if batch, err := s.Measure(nilCtx, opts...); err == nil || batch != nil {
			t.Fatalf("nil context accepted: %+v %v", batch, err)
		}
		inventory := append([]collect.OpOption{collect.WithGPUInventory()}, opts...)
		if batch, err := s.Measure(context.Background(), inventory...); err == nil || batch != nil {
			t.Fatalf("GPU inventory accepted: %+v %v", batch, err)
		}
	}
}

func TestQueueCycleAndTypedConnection(t *testing.T) {
	files := fstest.MapFS{"proc/sys/kernel/random/boot_id": {Data: []byte("boot-A")}, "sys/fs/fuse/connections/53/waiting": {Data: []byte("7")}}
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	cycle, err := collect.NewCycle("cycle-fuse", at)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := New(files).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(signals.Waiting.ID()))
	if err != nil || len(batch.Observations) != 1 || batch.CollectionId != "cycle-fuse" {
		t.Fatalf("%+v %v", batch, err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	o := batch.Observations[0]
	if o.Target.GetFuseConnection().ConnectionId != 53 || o.Target.GetFuseConnection().BootId != "boot-A" || o.GetValue().GetUintValue() != 7 || !o.ObservedAt.AsTime().Equal(at) {
		t.Fatalf("identity/value/time: %+v", o)
	}
}

func TestAbsentAndSelection(t *testing.T) {
	s := New(fstest.MapFS{})
	batch, err := s.Measure(context.Background(), collect.WithSignalID())
	if err != nil || len(batch.Observations) != 0 {
		t.Fatalf("%+v %v", batch, err)
	}
	if _, unknownErr := s.Measure(context.Background(), collect.WithSignalID("unknown")); unknownErr == nil {
		t.Fatal("accepted unknown selection")
	}
	batch, err = s.Measure(context.Background(), collect.WithSignalID(signals.Waiting.ID()))
	if err != nil || len(batch.Observations) != 1 || batch.Observations[0].GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
		t.Fatalf("%+v %v", batch, err)
	}
}
