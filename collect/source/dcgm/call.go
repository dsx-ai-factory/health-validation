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

var errSessionReplaced = &dcgmError{
	category: observation.CollectionErrorCategoryUnavailable,
	detail:   "DCGM session was replaced during collection",
}

// runNative serializes a timed operation on the collection's session. The
// worker records connection loss under the gate even if its caller timed out.
func runNative[T any](ctx context.Context, reader *collectionReader, call func(context.Context, *clientSession) (T, error)) (T, error) {
	source := reader.source
	return collect.Run(ctx, reader.cycle, Name, func(callContext context.Context) (value T, err error) {
		if err = source.startNativeCall(); err != nil {
			return value, err
		}
		defer source.finishNativeCall()
		if err = callContext.Err(); err != nil {
			return value, err
		}
		// A failed session admission ends native work for this collection, even
		// if an overlapping collection has since initialized successfully.
		if reader.admissionError != nil {
			return value, reader.admissionError
		}
		if err = reader.bindSession(callContext); err != nil {
			if reader.session == nil {
				reader.admissionError = err
			}
			return value, err
		}
		if source.closed.Load() {
			return value, errClosed
		}
		if err = callContext.Err(); err != nil {
			return value, err
		}
		session := reader.session
		defer func() {
			if source.session == session {
				session.recordConnectionFailure(reader.cycle, err)
			}
		}()
		return call(callContext, session)
	})
}

// bindSession keeps samples and identity lookups on one session. A restarted
// hostengine can assign the same numeric GPU ID to a different device.
// go-dcgm shares its native handle, so binding is checked under the native gate.
func (reader *collectionReader) bindSession(ctx context.Context) error {
	source := reader.source
	// An already-bound collection must keep its original session or stop.
	if reader.session != nil {
		if failure := reader.session.connectionFailure; failure != nil {
			return failure
		}
		if reader.session != source.session {
			return errSessionReplaced
		}
		return nil
	}
	if session := source.session; session != nil && session.connectionFailure != nil {
		// Standalone Source calls can create multiple readers for one Cycle.
		// Preserve a connection failure already observed in that collection.
		if session.connectionFailureCycle == reader.cycle {
			reader.session = session
			return session.connectionFailure
		}
		// This reader has not used a session, so it can recover from another
		// collection's failure, including an overlapping collection's failure.
		if err := source.releaseSession(); err != nil && !errors.Is(err, errConnectionNotValid) {
			return fmt.Errorf("release invalid DCGM session: %w", err)
		}
		// Cleanup can outlast the deadline; avoid opening a replacement then.
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := source.ensureInitialized(reader.cycle); err != nil {
		return err
	}
	reader.session = source.session
	return nil
}

// recordConnectionFailure retains the first confirmed loss in this session.
// The caller holds the native-call gate.
func (session *clientSession) recordConnectionFailure(cycle *collect.Cycle, err error) {
	if session.connectionFailure == nil && errors.Is(err, errConnectionNotValid) {
		session.connectionFailure = err
		session.connectionFailureCycle = cycle
	}
}

// recordFieldFailure recognizes connection loss reported as a per-field status.
func (session *clientSession) recordFieldFailure(cycle *collect.Cycle, failure *observation.CollectionError) {
	if failure.GetSourceError() == connectionNotValidStatus {
		session.recordConnectionFailure(cycle, &dcgmError{
			category: failure.GetCategory(), sourceError: failure.GetSourceError(), detail: failure.GetDetail(),
		})
	}
}
