// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultCollectionTimeout is the maximum time the collector waits for one
// source call. The underlying native call may continue afterward.
const DefaultCollectionTimeout = 60 * time.Second

var (
	// ErrCollectionTimeout means the collector stopped waiting for a source.
	ErrCollectionTimeout = errors.New("collection timed out")
	// ErrCollectionCanceled means the caller canceled collection while the
	// source call may still be running.
	ErrCollectionCanceled = errors.New("collection canceled")
)

// Cycle carries collection identity and timeout configuration for one
// collection pass. After a timeout, the source is skipped for the rest of
// that pass. The source implementation prevents native re-entry from a later
// pass while the timed-out call is still running.
type Cycle struct {
	startedAt     time.Time
	wedgedSources map[string]error
	collectionID  string
	timeout       time.Duration
	mu            sync.Mutex
}

func newCycle(collectionID string, startedAt time.Time, timeout time.Duration) (*Cycle, error) {
	if strings.TrimSpace(collectionID) == "" {
		return nil, fmt.Errorf("collection ID is required")
	}
	if startedAt.IsZero() {
		return nil, fmt.Errorf("cycle start time is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("collection timeout must be positive")
	}
	return &Cycle{
		collectionID:  collectionID,
		startedAt:     startedAt,
		timeout:       timeout,
		wedgedSources: make(map[string]error),
	}, nil
}

// CollectionID identifies observations produced by this collection pass.
func (cycle *Cycle) CollectionID() string {
	if cycle == nil {
		return ""
	}
	return cycle.collectionID
}

// StartedAt returns when this collection pass began.
func (cycle *Cycle) StartedAt() time.Time {
	if cycle == nil {
		return time.Time{}
	}
	return cycle.startedAt
}

// Run collects from one source with the cycle's timeout. A timeout or
// cancellation marks the source as wedged for the rest of this cycle. The
// source must still reject calls from later cycles while the native call is
// running.
func Run[T any](
	ctx context.Context,
	cycle *Cycle,
	source string,
	call func(context.Context) (T, error),
) (T, error) {
	var zero T
	if cycle == nil {
		return zero, fmt.Errorf("collection cycle is required")
	}
	if ctx == nil {
		return zero, fmt.Errorf("collection context is required")
	}
	if strings.TrimSpace(source) == "" {
		return zero, fmt.Errorf("source is required")
	}
	if call == nil {
		return zero, fmt.Errorf("collection callback is required")
	}
	if err := cycle.wedgedSource(source); err != nil {
		return zero, fmt.Errorf("source %q is wedged for this collection cycle: %w", source, err)
	}

	callContext, cancel := context.WithTimeout(ctx, cycle.timeout)
	defer cancel()
	if err := callContext.Err(); err != nil {
		return zero, sourceContextError(source, err)
	}

	type result struct {
		value T
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		current := result{}
		defer func() {
			if recovered := recover(); recovered != nil {
				current.err = fmt.Errorf("panic while collecting source %q: %v", source, recovered)
			}
			completed <- current
		}()
		current.value, current.err = call(callContext)
	}()

	select {
	case current := <-completed:
		if err := callContext.Err(); err != nil {
			return zero, cycle.markSourceWedged(source, sourceContextError(source, err))
		}
		return current.value, current.err
	case <-callContext.Done():
		return zero, cycle.markSourceWedged(
			source,
			sourceContextError(source, callContext.Err()),
		)
	}
}

func (cycle *Cycle) wedgedSource(source string) error {
	cycle.mu.Lock()
	defer cycle.mu.Unlock()
	return cycle.wedgedSources[source]
}

func (cycle *Cycle) markSourceWedged(source string, err error) error {
	cycle.mu.Lock()
	defer cycle.mu.Unlock()
	if previous := cycle.wedgedSources[source]; previous != nil {
		return previous
	}
	cycle.wedgedSources[source] = err
	return err
}

func sourceContextError(source string, err error) error {
	kind := ErrCollectionCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		kind = ErrCollectionTimeout
	}
	return fmt.Errorf("%w: source %q: %v", kind, source, err)
}
