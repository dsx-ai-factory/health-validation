// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// CallTracker tracks underlying collection calls beyond a watchdog timeout.
// A service can wait before admitting another cycle or closing its sources.
type CallTracker struct {
	active  map[*StuckCall]bool
	changed chan struct{}
	mu      sync.Mutex
	calls   sync.WaitGroup
}

type callTrackerKey struct{}

// WithCallTracking attaches a tracker to ctx. Call Wait only after collection
// has returned, when no new top-level calls can start under this context.
func WithCallTracking(ctx context.Context) (context.Context, *CallTracker) {
	tracker := new(CallTracker)
	return context.WithValue(ctx, callTrackerKey{}, tracker), tracker
}

// Wait waits for the underlying calls, including calls abandoned by watchdogs.
func (tracker *CallTracker) Wait() { tracker.calls.Wait() }

// StuckCall identifies an unfinished source call whose watchdog expired.
type StuckCall struct {
	Since  time.Time
	Source string
}

// StuckCalls returns calls whose watchdogs expired and a notification for the next
// tracking change. The returned values are independent of the tracker.
func (tracker *CallTracker) StuckCalls() ([]StuckCall, <-chan struct{}) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.changed == nil {
		tracker.changed = make(chan struct{})
	}
	var stuck []StuckCall
	for call, abandoned := range tracker.active {
		if abandoned {
			stuck = append(stuck, *call)
		}
	}
	slices.SortFunc(stuck, func(a, b StuckCall) int {
		if order := strings.Compare(a.Source, b.Source); order != 0 {
			return order
		}
		return a.Since.Compare(b.Since)
	})
	return stuck, tracker.changed
}

func (tracker *CallTracker) notify() {
	if tracker.changed != nil {
		close(tracker.changed)
	}
	tracker.changed = make(chan struct{})
}

// TrackSourceCall registers a named source call. finish clears the live call;
// abandon marks the unfinished call after its source watchdog expires.
func TrackSourceCall(ctx context.Context, source string) (finish, abandon func()) {
	tracker, _ := ctx.Value(callTrackerKey{}).(*CallTracker)
	if tracker == nil {
		return func() {}, func() {}
	}
	call := &StuckCall{Source: source, Since: time.Now()}
	tracker.calls.Add(1)
	tracker.mu.Lock()
	if tracker.active == nil {
		tracker.active = make(map[*StuckCall]bool)
	}
	tracker.active[call] = false
	tracker.notify()
	tracker.mu.Unlock()
	return func() {
			tracker.mu.Lock()
			delete(tracker.active, call)
			tracker.notify()
			tracker.mu.Unlock()
			tracker.calls.Done()
		}, func() {
			tracker.mu.Lock()
			if _, active := tracker.active[call]; active {
				tracker.active[call] = true
				tracker.notify()
			}
			tracker.mu.Unlock()
		}
}

// TrackSourceCallWithTimeout tracks work until it returns. The source watchdog
// keeps running after client cancellation, which alone does not mark work stuck.
func TrackSourceCallWithTimeout(ctx context.Context, source string, timeout time.Duration) func() {
	if _, ok := ctx.Value(callTrackerKey{}).(*CallTracker); !ok {
		return func() {}
	}
	finish, abandon := TrackSourceCall(ctx, source)
	timer := time.AfterFunc(timeout, abandon)
	return func() { timer.Stop(); finish() }
}
