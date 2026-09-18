// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type blockedDevice struct {
	device.Device
	started chan struct{}
	release chan struct{}
}

func (d *blockedDevice) GetPowerUsage() (uint32, nvml.Return) {
	close(d.started)
	<-d.release
	return 1000, nvml.SUCCESS
}

func TestCycleTimeoutAndCancellation(t *testing.T) {
	dev := &blockedDevice{started: make(chan struct{}), release: make(chan struct{})}
	defer close(dev.release)
	s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
	cycle, err := collect.NewCycleWithTimeout("first", time.Now(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *observation.ObservationBatch, 1)
	go func() {
		batch, _ := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()))
		done <- batch
	}()
	select {
	case <-dev.started:
	case <-time.After(5 * time.Second):
		t.Fatal("read never started")
	}
	var batch *observation.ObservationBatch
	select {
	case batch = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout not enforced")
	}
	if batch.GetCollectionId() != "first" || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout || !batch.Observations[0].ObservedAt.AsTime().Equal(cycle.StartedAt()) {
		t.Fatalf("timeout envelope: %v", batch)
	}
	next, err := collect.NewCycle("next", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch, err = s.Measure(context.Background(), collect.WithCycle(next), collect.WithSignalID(gpusignals.PowerDraw.ID()))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("native reentry: %v / %v", batch, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	empty := New(&fakeInstance{exists: true})
	batch, err = empty.Measure(ctx, collect.WithCycle(next))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("cancellation: %v / %v", batch, err)
	}
	batch, err = New(nil).Measure(context.Background(), collect.WithCycle(next), collect.WithSignalID(), collect.WithRefresh(true))
	if err != nil || len(batch.Observations) != 0 || batch.CollectionId != "next" {
		t.Fatalf("empty selection touched source: %v / %v", batch, err)
	}
}
