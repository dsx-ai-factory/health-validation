// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitTracker(t *testing.T, tracker *CallTracker) <-chan struct{} {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		tracker.Wait()
		close(finished)
	}()
	return finished
}

func requireTrackerBlocked(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
		t.Fatal("tracker returned while the underlying callback was still running")
	case <-time.After(25 * time.Millisecond):
	}
}

func requireTrackerFinished(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker did not return after underlying callbacks finished")
	}
}

func TestCallTrackerOutlivesWatchdogTimeout(t *testing.T) {
	cycle, err := NewCycleWithTimeout("tracked-timeout", time.Now(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, tracker := WithCallTracking(context.Background())
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	started := make(chan struct{})
	_, err = Run(ctx, cycle, "stuck", func(context.Context) (int, error) {
		close(started)
		<-release
		return 7, nil
	})
	if !errors.Is(err, ErrCollectionTimeout) {
		t.Fatalf("Run error = %v, want timeout", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("callback did not start")
	}
	finished := waitTracker(t, tracker)
	requireTrackerBlocked(t, finished)
	releaseOnce.Do(func() { close(release) })
	requireTrackerFinished(t, finished)
}

func TestCallTrackerIncludesNestedWorkAfterOuterCallbackReturns(t *testing.T) {
	cycle, err := NewCycleWithTimeout("tracked-nested", time.Now(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, tracker := WithCallTracking(context.Background())
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	outerReturned := make(chan struct{})
	innerStarted := make(chan struct{})
	_, err = Run(ctx, cycle, "outer", func(ctx context.Context) (int, error) {
		defer close(outerReturned)
		return Run(ctx, cycle, "inner", func(context.Context) (int, error) {
			close(innerStarted)
			<-release
			return 7, nil
		})
	})
	if !errors.Is(err, ErrCollectionTimeout) {
		t.Fatalf("Run error = %v, want timeout", err)
	}
	select {
	case <-outerReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("outer callback did not finish after its nested watchdog expired")
	}
	select {
	case <-innerStarted:
	default:
		t.Fatal("inner callback did not start")
	}
	finished := waitTracker(t, tracker)
	requireTrackerBlocked(t, finished)
	releaseOnce.Do(func() { close(release) })
	requireTrackerFinished(t, finished)
}

func TestCallTrackerCanceledBeforeCollectionStarts(t *testing.T) {
	cycle, err := NewCycleWithTimeout("tracked-canceled", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, tracker := WithCallTracking(ctx)
	cancel()
	var calls atomic.Int32
	_, err = Run(ctx, cycle, "canceled", func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	})
	if !errors.Is(err, ErrCollectionCanceled) {
		t.Fatalf("Run error = %v, want canceled collection", err)
	}
	requireTrackerFinished(t, waitTracker(t, tracker))
	if calls.Load() != 0 {
		t.Fatalf("callback ran %d times after cancellation", calls.Load())
	}
}

func TestTrackCallWithoutTrackingIsNoOp(t *testing.T) {
	TrackCall(context.Background())()
}
