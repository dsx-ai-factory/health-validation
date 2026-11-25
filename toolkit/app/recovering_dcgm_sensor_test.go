// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/sensorids"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type dcgmSensorFunc func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error)

func (dcgmSensorFunc) ID() string               { return sensorids.DCGM }
func (dcgmSensorFunc) Signals() collect.Signals { return dcgmGPUSignals }
func (f dcgmSensorFunc) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	return f(ctx, opts...)
}

func dcgmFailureBatch(code string) *observation.ObservationBatch {
	return &observation.ObservationBatch{Observations: []*observation.Observation{{
		Outcome: &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{SourceError: code}},
	}}}
}

func TestDCGMConnectionFailureKeepsSource(t *testing.T) {
	healthy := &observation.ObservationBatch{CollectionId: "healthy"}
	failed := dcgmFailureBatch("DCGM_ST_CONNECTION_NOT_VALID")
	var disconnected bool
	opens, closes := 0, 0
	sensor := &recoveringDCGMSensor{signals: dcgmGPUSignals, open: func() (collect.Sensor, func() error, func() bool, error) {
		opens++
		return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			if disconnected {
				return failed, nil
			}
			return healthy, nil
		}), func() error { closes++; return nil }, func() bool { return false }, nil
	}}
	if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
		t.Fatalf("initial read = %v, %v", got, err)
	}
	disconnected = true
	if got, err := sensor.Measure(context.Background()); err != nil || got != failed {
		t.Fatalf("failed read was hidden: %v, %v", got, err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("connection failure replaced source: opens=%d closes=%d", opens, closes)
	}
	disconnected = false
	for range 2 {
		if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
			t.Fatalf("recovered read = %v, %v", got, err)
		}
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("recovery replaced source: opens=%d closes=%d, want 1/0", opens, closes)
	}
	for range 2 {
		if err := sensor.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if closes != 1 {
		t.Fatalf("close calls=%d, want one for the reused source", closes)
	}
}

func TestDCGMFailedWatchSetupRecreatesSourceNextCollection(t *testing.T) {
	failed := dcgmFailureBatch("DCGM_ST_NO_PERMISSION")
	healthy := &observation.ObservationBatch{CollectionId: "healthy"}
	opens, closes := 0, 0
	sensor := &recoveringDCGMSensor{open: func() (collect.Sensor, func() error, func() bool, error) {
		opens++
		firstSession := opens == 1
		closed := false
		return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			if firstSession {
				closed = true
				return failed, nil
			}
			return healthy, nil
		}), func() error { closes++; return nil }, func() bool { return closed }, nil
	}}
	defer sensor.Close()

	if got, err := sensor.Measure(context.Background()); err != nil || got != failed {
		t.Fatalf("failed setup = %v, %v", got, err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("replaced source during failed collection: opens=%d closes=%d", opens, closes)
	}
	for range 2 {
		if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
			t.Fatalf("read after failed setup = %v, %v", got, err)
		}
	}
	if opens != 2 || closes != 1 {
		t.Fatalf("source replacement or reuse failed: opens=%d closes=%d, want 2/1", opens, closes)
	}
}

func TestDCGMOtherErrorsKeepSession(t *testing.T) {
	for _, code := range []string{"DCGM_ST_NO_PERMISSION", "DCGM_ST_GPU_IS_LOST", "DCGM_ST_NO_DATA"} {
		t.Run(code, func(t *testing.T) {
			opens, closes := 0, 0
			failed := dcgmFailureBatch(code)
			sensor := &recoveringDCGMSensor{open: func() (collect.Sensor, func() error, func() bool, error) {
				opens++
				return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) { return failed, nil }),
					func() error { closes++; return nil }, func() bool { return false }, nil
			}}
			defer sensor.Close()
			for range 2 {
				if got, err := sensor.Measure(context.Background()); err != nil || got != failed {
					t.Fatalf("read = %v, %v", got, err)
				}
			}
			if opens != 1 || closes != 0 {
				t.Fatalf("non-connection failure replaced session: opens=%d closes=%d", opens, closes)
			}
		})
	}
}

func TestDCGMClosedSourceWaitsForBusyCleanup(t *testing.T) {
	opens, cleanupCalls := 0, 0
	busy := true
	failed := dcgmFailureBatch("DCGM_ST_NO_PERMISSION")
	healthy := &observation.ObservationBatch{CollectionId: "recovered"}
	sensor := &recoveringDCGMSensor{open: func() (collect.Sensor, func() error, func() bool, error) {
		opens++
		firstSource := opens == 1
		closed := false
		return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
				if firstSource {
					closed = true
					return failed, nil
				}
				return healthy, nil
			}), func() error {
				cleanupCalls++
				if busy {
					return dcgmsource.ErrInUse
				}
				return nil
			}, func() bool { return closed }, nil
	}}
	if got, err := sensor.Measure(context.Background()); err != nil || got != failed {
		t.Fatalf("failed setup = %v, %v", got, err)
	}
	if _, err := sensor.Measure(context.Background()); !errors.Is(err, dcgmsource.ErrInUse) {
		t.Fatalf("busy cleanup = %v", err)
	}
	if opens != 1 || cleanupCalls != 1 {
		t.Fatal("opened a replacement before old native work finished")
	}
	busy = false
	if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
		t.Fatalf("read after cleanup = %v, %v", got, err)
	}
	if opens != 2 || cleanupCalls != 2 {
		t.Fatalf("replacement opens=%d cleanup=%d, want 2/2", opens, cleanupCalls)
	}
	if err := sensor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDCGMCanceledReadKeepsSource(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	opens, closes := 0, 0
	firstRead := true
	healthy := &observation.ObservationBatch{CollectionId: "recovered"}
	sensor := &recoveringDCGMSensor{open: func() (collect.Sensor, func() error, func() bool, error) {
		opens++
		return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			if firstRead {
				firstRead = false
				close(started)
				<-release
				return dcgmFailureBatch("DCGM_ST_CONNECTION_NOT_VALID"), nil
			}
			return healthy, nil
		}), func() error { closes++; return nil }, nil, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracked, tracker := collect.WithCallTracking(ctx)
	cycle, err := collect.NewCycleWithTimeout("recovery", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := collect.Run(tracked, cycle, sensor.ID(), func(ctx context.Context) (*observation.ObservationBatch, error) { return sensor.Measure(ctx) })
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("native read did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, collect.ErrCollectionCanceled) {
		t.Fatal(err)
	}
	if err := sensor.Close(); !errors.Is(err, dcgmsource.ErrInUse) {
		t.Fatalf("close entered unfinished read: %v", err)
	}
	if _, err := sensor.Measure(context.Background()); !errors.Is(err, dcgmsource.ErrInUse) {
		t.Fatalf("second read overlapped: %v", err)
	}
	if opens != 1 || closes != 0 {
		t.Fatal("source was replaced while native work was active")
	}
	close(release)
	tracker.Wait()
	if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
		t.Fatalf("read after cancellation = %v, %v", got, err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("late connection failure replaced source: opens=%d closes=%d, want 1/0", opens, closes)
	}
	if err := sensor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDCGMReopensAfterClosedSourceCleanupFailure(t *testing.T) {
	opens, closes := 0, 0
	failed := dcgmFailureBatch("DCGM_ST_NO_PERMISSION")
	healthy := &observation.ObservationBatch{CollectionId: "recovered"}
	sensor := &recoveringDCGMSensor{open: func() (collect.Sensor, func() error, func() bool, error) {
		opens++
		firstSource := opens == 1
		closed := false
		return dcgmSensorFunc(func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
				if firstSource {
					closed = true
					return failed, nil
				}
				return healthy, nil
			}), func() error {
				closes++
				if firstSource {
					return errors.New("old hostengine watch no longer exists")
				}
				return nil
			}, func() bool { return closed }, nil
	}}
	if got, err := sensor.Measure(context.Background()); err != nil || got != failed {
		t.Fatalf("failed setup = %v, %v", got, err)
	}
	if got, err := sensor.Measure(context.Background()); err != nil || got != healthy {
		t.Fatalf("read after cleanup failure = %v, %v", got, err)
	}
	if opens != 2 || closes != 1 {
		t.Fatalf("replacement opens=%d closes=%d, want 2/1", opens, closes)
	}
	if err := sensor.Close(); err != nil {
		t.Fatal(err)
	}
}
