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
	require.Equal(t, "mlx5_0/1", batch.Observations[0].GetEntity().GetId())
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
		require.Contains(t, []string{"mlx5_0/1", "mlx5_1/1", "mlx5_2/1", "mlx5_3/1", "mlx5_4/1", "mlx5_5/1", "mlx5_6/1"}, obs.GetEntity().GetId())
	}
	// Re-read identity each cycle; do not retain a stale IB classification.
	files["mlx5_0/ports/1/link_layer"].Data = []byte("Ethernet")
	delete(files, "mlx5_1/ports/1/link_layer")
	batch, err = sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 6)
	require.Equal(t, "mlx5_1/1", batch.Observations[0].GetEntity().GetId())
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().GetCategory())
	require.Equal(t, "mlx5_1/ports/1/link_layer", batch.Observations[0].GetCollectionError().GetOperation())
}
