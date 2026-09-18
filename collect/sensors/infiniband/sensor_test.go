// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
)

func TestMeasure(t *testing.T) {
	cycle, err := collect.NewCycle("cycle", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := New(ibsource.New(fstest.MapFS{"mlx5_0/ports/1/state": {Data: []byte("4: ACTIVE")}}))
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID(), ibsignals.Rate.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	if batch.CollectionId != "cycle" || len(batch.Observations) != 2 || batch.Observations[0].GetEntity().GetId() != "mlx5_0/1" || batch.Observations[0].GetValue().GetIntValue() != 4 || batch.Observations[1].GetCollectionError() == nil {
		t.Fatalf("batch: %v", batch)
	}
	if _, measureErr := s.Measure(context.Background()); measureErr == nil {
		t.Fatal("accepted missing cycle")
	}
	if _, measureErr := s.Measure(context.Background(), collect.WithSignalID("unknown")); measureErr == nil {
		t.Fatal("accepted unknown signal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	batch, err = s.Measure(ctx, collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("cancellation: %v / %v", batch, err)
	}
}
