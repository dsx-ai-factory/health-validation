// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
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
	s := New(ibsource.New(fstest.MapFS{"mlx5_0/ports/1/state": {Data: []byte("4: ACTIVE")}}))
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
