// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"sync"
)

// CallTracker tracks underlying collection calls beyond a watchdog timeout.
// A service can wait before admitting another cycle or closing its sources.
type CallTracker struct {
	calls sync.WaitGroup
}

type callTrackerKey struct{}

// WithCallTracking attaches a tracker to ctx. Call Wait only after collection
// has returned, when no new top-level calls can start under this context.
func WithCallTracking(ctx context.Context) (context.Context, *CallTracker) {
	tracker := new(CallTracker)
	return context.WithValue(ctx, callTrackerKey{}, tracker), tracker
}

// TrackCall registers an underlying call and returns its completion function.
// Without a tracker the returned function is a no-op.
func TrackCall(ctx context.Context) func() {
	tracker, _ := ctx.Value(callTrackerKey{}).(*CallTracker)
	if tracker == nil {
		return func() {}
	}
	tracker.calls.Add(1)
	return tracker.calls.Done
}

// Wait waits for the underlying calls, including calls abandoned by watchdogs.
func (tracker *CallTracker) Wait() { tracker.calls.Wait() }
