// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunReturnsCompletedValue(t *testing.T) {
	current, err := NewCycleWithTimeout("completed", time.Now().UTC(), time.Second)
	if err != nil {
		t.Fatalf("NewCycleWithTimeout() error = %v", err)
	}

	value, err := Run(context.Background(), current, "dcgm", func(context.Context) (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if value != 42 {
		t.Fatalf("Run() value = %d, want 42", value)
	}
}

func TestRunDoesNotStartOrWedgeSourceWhenContextIsAlreadyCanceled(t *testing.T) {
	current, err := NewCycleWithTimeout("canceled", time.Now().UTC(), time.Second)
	if err != nil {
		t.Fatalf("NewCycleWithTimeout() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	_, err = Run(ctx, current, "dcgm", func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	})
	if !errors.Is(err, ErrCollectionCanceled) {
		t.Fatalf("Run() error = %v, want %v", err, ErrCollectionCanceled)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled callback ran %d times, want 0", calls.Load())
	}

	value, err := Run(context.Background(), current, "dcgm", func(context.Context) (int, error) {
		return 2, nil
	})
	if err != nil {
		t.Fatalf("Run() after canceled call error = %v", err)
	}
	if value != 2 {
		t.Fatalf("Run() after canceled call value = %d, want 2", value)
	}
}

func TestRunTimeoutWedgesOnlyTimedOutSource(t *testing.T) {
	current, err := NewCycleWithTimeout("timeout", time.Now().UTC(), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewCycleWithTimeout() error = %v", err)
	}
	callStarted := make(chan struct{})
	releaseCall := make(chan struct{})
	releaseTimedOutCall := func() {
		select {
		case <-releaseCall:
		default:
			close(releaseCall)
		}
	}
	t.Cleanup(releaseTimedOutCall)

	_, err = Run(context.Background(), current, "dcgm", func(context.Context) (int, error) {
		close(callStarted)
		<-releaseCall
		return 1, nil
	})
	if !errors.Is(err, ErrCollectionTimeout) {
		t.Fatalf("Run() error = %v, want %v", err, ErrCollectionTimeout)
	}
	select {
	case <-callStarted:
	default:
		t.Fatal("timed-out call did not start")
	}

	var repeatedCalls atomic.Int32
	_, err = Run(context.Background(), current, "dcgm", func(context.Context) (int, error) {
		repeatedCalls.Add(1)
		return 2, nil
	})
	if !errors.Is(err, ErrCollectionTimeout) {
		t.Fatalf("second DCGM Run() error = %v, want original timeout", err)
	}
	if repeatedCalls.Load() != 0 {
		t.Fatalf("second DCGM callback ran %d times, want 0", repeatedCalls.Load())
	}

	value, err := Run(context.Background(), current, "nvml", func(context.Context) (int, error) {
		return 3, nil
	})
	if err != nil {
		t.Fatalf("NVML Run() error = %v", err)
	}
	if value != 3 {
		t.Fatalf("NVML Run() value = %d, want 3", value)
	}
	releaseTimedOutCall()
}
