// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

func TestGPMMultipleGPUsShareSingleWait(t *testing.T) {
	first, second, unsupported := &profilingDevice{supported: 1}, &profilingDevice{supported: 1}, &profilingDevice{}
	public, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"first": first, "second": second, "unsupported": unsupported}}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	sensor := public.(*gpmSensor)
	var samples []*profilingSample
	sensor.alloc = func() (nvml.GpmSample, nvml.Return) {
		sample := &profilingSample{}
		samples = append(samples, sample)
		return sample, nvml.SUCCESS
	}
	waits := 0
	sensor.wait = func(ctx context.Context, duration time.Duration) error {
		waits++
		require.Equal(t, time.Second, duration)
		require.Equal(t, 1, first.sampleCalls)
		require.Equal(t, 1, second.sampleCalls)
		require.Zero(t, unsupported.sampleCalls)
		return ctx.Err()
	}
	queries := 0
	sensor.getMetrics = func(query *nvml.GpmMetricsGetType) nvml.Return {
		queries++
		query.Metrics[0].Value = 50
		return nvml.SUCCESS
	}
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Equal(t, 1, waits)
	require.Equal(t, 2, queries)
	require.Equal(t, 2, first.sampleCalls)
	require.Equal(t, 2, second.sampleCalls)
	require.Len(t, batch.Observations, 3)
	require.Len(t, samples, 4)
	for _, sample := range samples {
		require.Equal(t, 1, sample.freed)
	}
}
