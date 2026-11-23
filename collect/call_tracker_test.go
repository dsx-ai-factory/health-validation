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

func TestCallTrackerOutlivesCancellation(t *testing.T) {
	cycle, err := NewCycleWithTimeout("tracked-timeout", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, tracker := WithCallTracking(ctx)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	_, err = Run(ctx, cycle, "stuck", func(context.Context) (int, error) {
		close(started)
		<-release
		return 7, nil
	})
	if !errors.Is(err, ErrCollectionCanceled) {
		t.Fatalf("Run error = %v, want canceled collection", err)
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
	cycle, err := NewCycleWithTimeout("tracked-nested", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, tracker := WithCallTracking(ctx)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	outerReturned := make(chan struct{})
	innerStarted := make(chan struct{})
	go func() {
		<-innerStarted
		cancel()
	}()
	_, err = Run(ctx, cycle, "outer", func(ctx context.Context) (int, error) {
		defer close(outerReturned)
		return Run(ctx, cycle, "inner", func(context.Context) (int, error) {
			close(innerStarted)
			<-release
			return 7, nil
		})
	})
	if !errors.Is(err, ErrCollectionCanceled) {
		t.Fatalf("Run error = %v, want canceled collection", err)
	}
	select {
	case <-outerReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("outer callback did not finish after cancellation")
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

func TestTrackSourceCallWithoutTrackingIsNoOp(t *testing.T) {
	finish, abandon := TrackSourceCall(context.Background(), "source")
	abandon()
	finish()
}

func TestCallTrackerReportsAbandonedSourcesAndClearsThem(t *testing.T) {
	ctx, tracker := WithCallTracking(context.Background())
	finishZ, abandonZ := TrackSourceCall(ctx, "source-z")
	finishA, abandonA := TrackSourceCall(ctx, "source-a")
	var closeZ, closeA sync.Once
	defer closeZ.Do(finishZ)
	defer closeA.Do(finishA)
	live, changed := tracker.StuckCalls()
	if len(live) != 0 {
		t.Fatalf("healthy in-flight calls reported as stuck: %v", live)
	}
	select {
	case <-changed:
		t.Fatal("notification closed before a change")
	default:
	}
	abandonZ()
	abandonA()
	select {
	case <-changed:
	default:
		t.Fatal("abandonment did not notify observers")
	}
	stuck, changed := tracker.StuckCalls()
	if len(stuck) != 2 || stuck[0].Source != "source-a" || stuck[1].Source != "source-z" || stuck[0].Since.IsZero() || stuck[1].Since.IsZero() {
		t.Fatalf("source metadata = %v", stuck)
	}
	stuck[0].Source = "mutated"
	unchanged, _ := tracker.StuckCalls()
	if unchanged[0].Source != "source-a" {
		t.Fatal("caller changed tracker metadata through its snapshot")
	}
	closeA.Do(finishA)
	select {
	case <-changed:
	default:
		t.Fatal("call completion did not notify observers")
	}
	remaining, _ := tracker.StuckCalls()
	if len(remaining) != 1 || remaining[0].Source != "source-z" {
		t.Fatalf("remaining calls = %v", remaining)
	}
	closeZ.Do(finishZ)
	abandonZ()
	remaining, _ = tracker.StuckCalls()
	if len(remaining) != 0 {
		t.Fatalf("completed source remained stuck: %v", remaining)
	}
	tracker.Wait()
}
