// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

func TestMeasureMetricTimeoutPreservesGPUValuesAndUUIDs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		source := dcgmsource.NewMetricTimeoutSourceForTest(started, release)
		defer func() {
			close(release)
			synctest.Wait()
			if err := source.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}()
		sensor, err := dcgmsensor.New(source, gpusignals.PowerDraw.ID())
		if err != nil {
			t.Fatal(err)
		}
		const timeout = 100 * time.Millisecond
		cycle, err := collect.NewCycleWithTimeout("measure-partial-metrics", time.Now(), timeout)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan *observation.ObservationBatch, 1)
		go func() {
			batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle))
			if err != nil {
				t.Errorf("Measure() error = %v", err)
			}
			finished <- batch
		}()
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("GPU 1's native metric read did not start")
		}
		select {
		case <-finished:
			t.Fatal("Measure returned before the blocked native read timed out")
		default:
		}

		time.Sleep(timeout)
		synctest.Wait()
		var batch *observation.ObservationBatch
		select {
		case batch = <-finished:
		default:
			t.Fatal("Measure did not return after the collection timeout")
		}
		if err := observation.ValidateBatch(batch); err != nil {
			t.Fatal(err)
		}
		if batch.GetCollectionId() != cycle.CollectionID() || len(batch.GetObservations()) != 2 {
			t.Fatalf("partial batch = %v", batch)
		}
		sampled, failed := batch.Observations[0], batch.Observations[1]
		if sampled.GetValue() == nil || sampled.GetValue().GetDoubleValue() != 100 || sampled.GetCollectionError() != nil ||
			sampled.GetTarget().GetGpu().GetUuid() != "GPU-0" {
			t.Fatalf("GPU 0 lost its completed value or UUID: %v", sampled)
		}
		failure := failed.GetCollectionError()
		if failed.GetValue() != nil || failed.GetTarget().GetGpu().GetUuid() != "GPU-1" ||
			failure.GetCategory() != observation.CollectionErrorCategoryTimeout || failure.GetSource() != dcgmsource.Name ||
			failure.GetOperation() != "metrics" {
			t.Fatalf("GPU 1 lost its timeout or UUID: %v", failed)
		}
	})
}
