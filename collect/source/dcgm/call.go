// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// runNative initializes and serializes a timed native operation. Connection loss
// is recorded before unlocking, even when its caller has already timed out.
func runNative[T any](ctx context.Context, cycle *collect.Cycle, source *Source, call func(context.Context) (T, error)) (T, error) {
	return collect.Run(ctx, cycle, Name, func(callContext context.Context) (value T, err error) {
		if err = source.startNativeCall(); err != nil {
			return value, err
		}
		defer source.finishNativeCall()
		defer func() { source.recordConnectionFailure(cycle, err) }()
		if err = callContext.Err(); err != nil {
			return value, err
		}
		// Return a known connection failure throughout its collection cycle.
		// A later cycle releases the old session before initializing a replacement.
		if source.connectionFailure != nil {
			if source.connectionFailureCycle == cycle {
				return value, source.connectionFailure
			}
			if err = source.releaseInvalidSession(); err != nil {
				return value, err
			}
			if err = callContext.Err(); err != nil {
				return value, err
			}
		}
		if err = source.ensureInitialized(cycle); err != nil {
			return value, err
		}
		if source.closed.Load() {
			return value, errClosed
		}
		if err = callContext.Err(); err != nil {
			return value, err
		}
		return call(callContext)
	})
}

// recordConnectionFailure retains the first confirmed loss in this session.
// The caller holds the native-call gate.
func (source *Source) recordConnectionFailure(cycle *collect.Cycle, err error) {
	// Failed initialization owns no session; ensureInitialized scopes its retry.
	if source.ready && source.connectionFailure == nil && errors.Is(err, errConnectionNotValid) {
		source.connectionFailure = err
		source.connectionFailureCycle = cycle
	}
}

// recordFieldFailure recognizes connection loss reported as a per-field status.
func (source *Source) recordFieldFailure(cycle *collect.Cycle, failure *observation.CollectionError) {
	if failure.GetSourceError() == connectionNotValidStatus {
		source.recordConnectionFailure(cycle, &dcgmError{
			category: failure.GetCategory(), sourceError: failure.GetSourceError(), detail: failure.GetDetail(),
		})
	}
}

// releaseInvalidSession discards stale handles before releasing the old client.
// Releasing a known-invalid connection may itself report connection loss.
func (source *Source) releaseInvalidSession() error {
	cleanup := source.clientCleanup
	source.clientCleanup = nil
	source.ready = false
	source.metricWatchCleanup = nil
	source.metricWatchError = nil
	source.healthWatch = nil
	source.connectionFailure = nil
	source.connectionFailureCycle = nil
	if cleanup != nil {
		if err := cleanup(); err != nil && !errors.Is(err, errConnectionNotValid) {
			return fmt.Errorf("release invalid DCGM session: %w", err)
		}
	}
	return nil
}
