// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

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
	synctest.Test(t, func(t *testing.T) {
		dev := &blockedDevice{started: make(chan struct{}), release: make(chan struct{})}
		defer close(dev.release)
		s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
		cycle, err := collect.NewCycleWithTimeout("first", time.Now(), time.Second)
		require.NoError(t, err)
		done := make(chan *observation.ObservationBatch, 1)
		go func() {
			batch, _ := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()))
			done <- batch
		}()
		select {
		case <-dev.started:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "read never started")
		}
		var batch *observation.ObservationBatch
		select {
		case batch = <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "timeout not enforced")
		}
		require.Equal(t, "first", batch.GetCollectionId())
		require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[0].GetCollectionError().GetCategory())
		require.True(t, batch.Observations[0].ObservedAt.AsTime().Equal(cycle.StartedAt()))
		next, err := collect.NewCycle("next", time.Now())
		require.NoError(t, err)
		batch, err = s.Measure(context.Background(), collect.WithCycle(next), collect.WithSignalID(gpusignals.PowerDraw.ID()))
		require.NoError(t, err)
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		empty := New(&fakeInstance{exists: true})
		batch, err = empty.Measure(ctx, collect.WithCycle(next), collect.WithSignalID(gpusignals.PowerDraw.ID()))
		require.NoError(t, err)
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
		batch, err = New(nil).Measure(context.Background(), collect.WithCycle(next), collect.WithSignalID(), collect.WithRefresh(true))
		require.NoError(t, err)
		require.Empty(t, batch.Observations)
		require.Equal(t, "next", batch.CollectionId)
	})
}
