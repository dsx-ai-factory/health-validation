// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
	source "github.com/dsx-ai-factory/health-validation/collect/source/disk"
)

func TestInvalidRequestRejectedBeforeCollection(t *testing.T) {
	s := New(fstest.MapFS{})
	for _, opts := range [][]collect.OpOption{nil, {collect.WithSignalID()}} {
		if batch, err := s.Measure(nil, opts...); err == nil || batch != nil {
			t.Fatalf("nil context accepted: %+v %v", batch, err)
		}
		inventory := append([]collect.OpOption{collect.WithGPUInventory()}, opts...)
		if batch, err := s.Measure(context.Background(), inventory...); err == nil || batch != nil {
			t.Fatalf("GPU inventory accepted: %+v %v", batch, err)
		}
	}
}

func TestCycleAndMountIdentity(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("7 1 8:1 / /mnt\\040disk rw,nosuid - ext4 /dev/sda1 rw\n")}}
	s := New(files, source.WithNamespace("mnt:[9]"))
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	cycle, err := collect.NewCycle("cycle-disk", at)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(signals.MountPoint.ID(), signals.MountDevice.ID()))
	if err != nil || batch.CollectionId != "cycle-disk" || len(batch.Observations) != 2 {
		t.Fatalf("%+v %v", batch, err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	for _, o := range batch.Observations {
		if o.Target.GetMount().MountId != 7 || o.Target.GetMount().Namespace != "mnt:[9]" || !o.ObservedAt.AsTime().Equal(at) {
			t.Fatalf("identity or cycle lost: %+v", o)
		}
	}
	if batch.Observations[0].GetValue().GetStringValue() != "/mnt disk" {
		t.Fatal(batch.Observations[0])
	}
}

func TestOptionalCycleEmptyAndUnknownSelection(t *testing.T) {
	s := New(fstest.MapFS{})
	batch, err := s.Measure(context.Background(), collect.WithSignalID())
	if err != nil || len(batch.Observations) != 0 {
		t.Fatalf("empty: %+v %v", batch, err)
	}
	if _, err := s.Measure(context.Background(), collect.WithSignalID("unknown")); err == nil {
		t.Fatal("accepted unknown selection")
	}
	batch, err = s.Measure(context.Background(), collect.WithSignalID(signals.Name.ID()))
	if err != nil || len(batch.Observations) != 1 || batch.Observations[0].GetCollectionError() == nil {
		t.Fatalf("optional cycle: %+v %v", batch, err)
	}
}
