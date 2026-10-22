// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
)

func TestMeasure(t *testing.T) {
	cycle, err := collect.NewCycle("cycle", time.Now())
	require.NoError(t, err)
	s := New(ibsource.New(fstest.MapFS{"mlx5_0/ports/1/link_layer": {Data: []byte("InfiniBand")}, "mlx5_0/ports/1/state": {Data: []byte("4: ACTIVE")}}))
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID(), ibsignals.Rate.ID()))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Equal(t, "cycle", batch.CollectionId)
	require.Len(t, batch.Observations, 2)
	for _, obs := range batch.Observations {
		require.Equal(t, "mlx5_0", obs.GetTarget().GetInfiniBandPort().GetDevice().GetName())
		require.Equal(t, uint32(1), obs.GetTarget().GetInfiniBandPort().GetPort())
	}
	require.Equal(t, int64(4), batch.Observations[0].GetValue().GetIntValue())
	require.NotNil(t, batch.Observations[1].GetCollectionError())
	_, err = s.Measure(context.Background())
	require.Error(t, err)
	_, err = s.Measure(context.Background(), collect.WithSignalID("unknown"))
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	batch, err = s.Measure(ctx, collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
}

func TestEmptySelectionRequiresCycle(t *testing.T) {
	s := New(nil)
	_, err := s.Measure(context.Background(), collect.WithSignalID())
	require.Error(t, err)
	cycle, err := collect.NewCycle("empty", time.Now())
	require.NoError(t, err)
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID())
	require.NoError(t, err)
	require.Equal(t, "empty", batch.GetCollectionId())
	require.Empty(t, batch.GetObservations())
}

func TestLinkLayerFiltering(t *testing.T) {
	files := fstest.MapFS{}
	// Seven IB ports plus one Ethernet port must never count as eight IB ports.
	for i, layer := range []string{"InfiniBand", "infiniband", "INFINIBAND", "InfiniBand", "InfiniBand", "InfiniBand", "InfiniBand", "Ethernet", "Unknown", ""} {
		base := fmt.Sprintf("mlx5_%d/ports/1/", i)
		files[base+"link_layer"] = &fstest.MapFile{Data: []byte(layer)}
		files[base+"state"] = &fstest.MapFile{Data: []byte("4: ACTIVE")}
		files[base+"rate"] = &fstest.MapFile{Data: []byte("400 Gb/sec (4X NDR)")}
	}
	cycle, err := collect.NewCycle("mixed", time.Now())
	require.NoError(t, err)
	sensor := New(ibsource.New(files))
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID(), ibsignals.Rate.ID()))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Len(t, batch.Observations, 14)
	for _, obs := range batch.Observations {
		require.NotNil(t, obs.GetValue())
		require.Contains(t, []string{"mlx5_0", "mlx5_1", "mlx5_2", "mlx5_3", "mlx5_4", "mlx5_5", "mlx5_6"}, obs.GetTarget().GetInfiniBandPort().GetDevice().GetName())
		require.Equal(t, uint32(1), obs.GetTarget().GetInfiniBandPort().GetPort())
	}
	// WithLinkLayers admits RoCE ports, matched case-insensitively; Unknown
	// and empty layers stay excluded, and the default is unchanged.
	roce := New(ibsource.New(files), WithLinkLayers("InfiniBand", "ethernet"))
	batch, err = roce.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID(), ibsignals.Rate.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 16)
	devices := map[string]bool{}
	for _, obs := range batch.Observations {
		devices[obs.GetTarget().GetInfiniBandPort().GetDevice().GetName()] = true
	}
	require.True(t, devices["mlx5_7"])
	require.False(t, devices["mlx5_8"])
	require.False(t, devices["mlx5_9"])
	ethernetOnly := New(ibsource.New(files), WithLinkLayers("Ethernet"))
	batch, err = ethernetOnly.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 1)
	require.Equal(t, "mlx5_7", batch.Observations[0].GetTarget().GetInfiniBandPort().GetDevice().GetName())

	// Re-read identity each cycle; do not retain a stale IB classification.
	files["mlx5_0/ports/1/link_layer"].Data = []byte("Ethernet")
	delete(files, "mlx5_1/ports/1/link_layer")
	batch, err = sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 6)
	require.Equal(t, "mlx5_1", batch.Observations[0].GetTarget().GetInfiniBandPort().GetDevice().GetName())
	require.Equal(t, uint32(1), batch.Observations[0].GetTarget().GetInfiniBandPort().GetPort())
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
	require.Equal(t, "mlx5_1/ports/1/link_layer", batch.Observations[0].GetCollectionError().GetOperation())
}

func TestMeasureNoValue(t *testing.T) {
	files := fstest.MapFS{
		"mlx5_0/fw_ver":                            {Data: []byte("28.41.1000\n")},
		"mlx5_0/ports/2/link_layer":                {Data: []byte("InfiniBand")},
		"mlx5_0/ports/2/hw_counters/out_of_buffer": {Data: []byte(" \tN/A (no PMA)\n")},
		"mlx5_1/ports/2/link_layer":                {Data: []byte("Ethernet")},
		"mlx5_1/ports/2/hw_counters/out_of_buffer": {Data: []byte("N/A (no PMA)")},
	}
	cycle, err := collect.NewCycle("no-value", time.Now())
	require.NoError(t, err)
	s := New(ibsource.New(files))
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.HardwareOutOfBuffer.ID()))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Len(t, batch.Observations, 1)
	obs := batch.Observations[0]
	require.Equal(t, ibsignals.HardwareOutOfBuffer.ID(), obs.GetSignalId())
	require.Equal(t, ibsignals.HardwareOutOfBuffer.Unit(), obs.Unit)
	require.Equal(t, ibsource.Name, obs.GetSource())
	require.True(t, cycle.StartedAt().Equal(obs.GetObservedAt().AsTime()))
	require.Equal(t, "mlx5_0", obs.GetTarget().GetInfiniBandPort().GetDevice().GetName())
	require.Equal(t, uint32(2), obs.GetTarget().GetInfiniBandPort().GetPort())
	require.Nil(t, obs.GetValue())
	require.Nil(t, obs.GetCollectionError())
	require.Equal(t, observation.NoValueReasonNotSupported, obs.GetNoValue().GetReason())
	require.Equal(t, "N/A (no PMA)", obs.GetNoValue().GetSourceStatus())
	require.Equal(t, "N/A (no PMA)", obs.GetNoValue().GetDetail())

	batch, err = s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.FirmwareVersion.ID()))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))
	require.Len(t, batch.Observations, 2)
	require.Equal(t, "mlx5_0", batch.Observations[0].GetTarget().GetInfiniBandDevice().GetName())
	require.Equal(t, "28.41.1000", batch.Observations[0].GetValue().GetStringValue())
	require.Equal(t, "mlx5_1", batch.Observations[1].GetTarget().GetInfiniBandDevice().GetName())
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[1].GetCollectionError().GetCategory())
}
