// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// Name identifies DCGM as an observation source.
const Name = "dcgm"

const defaultGroupNamePrefix = "shared-signal-collection"

// ErrInUse indicates that a native call is already active and cannot be
// canceled safely.
var ErrInUse = errors.New("DCGM native call already in progress")

var errClosed = errors.New("DCGM source is closed")

// This process-wide lock prevents another call from entering DCGM while a
// timed-out cgo call may still be running. go-dcgm already coordinates
// Init and Shutdown, so this package does not add another reference count.
//
// TODO: Before adding another DCGM sensor, move native-call and watch ownership
// to each sensor's collection path. Independent sensors should not block each
// other, and sensors that need GPU identities should share one immutable
// inventory snapshot per collection cycle. DCGM initialization and shutdown
// must remain coordinated process-wide.
var defaultNativeCallMu sync.Mutex

// go-dcgm ignores the mode and address of every Init while an earlier Init is
// still open, so sources record the first successful mode here and reject any
// other mode. While the first source is open, a later standalone source with
// another address reads the first source's hostengine. Sources open at the same
// time share one DCGM connection and its field watches, so closing one stops
// the field updates the others read; keep one source open per process. The mode
// stays set after every source closes, because re-initializing libdcgm in
// another mode after go-dcgm's final Shutdown is untested. It is read and
// written only while holding defaultNativeCallMu.
var defaultProcessMode sessionMode

// StandaloneOptions configures a connection to an external DCGM hostengine.
type StandaloneOptions struct {
	// Address is the hostengine endpoint. Empty defaults to localhost.
	Address string
	// GroupNamePrefix identifies this consumer in DCGM field-group listings.
	// The source appends the process ID and a process-local sequence before the
	// "-fields" suffix. Empty uses "shared-signal-collection".
	GroupNamePrefix string
	// IsSocket indicates that Address is a Unix domain socket path.
	IsSocket bool
	// LiveMetrics reads metric fields from the driver on each read instead of
	// the hostengine's cache. A cached sample can be as old as the watch
	// interval of whichever client already watches the field, since a new
	// watch does not trigger a sample. DCGM fails the whole read if any
	// requested field does not support live reads.
	LiveMetrics bool
}

// EmbeddedOptions configures DCGM started inside this process.
type EmbeddedOptions struct {
	// GroupNamePrefix names this source's DCGM field group. An embedded engine
	// opens no listener, so no other process can list the group. The source
	// appends the process ID and a process-local sequence before the "-fields"
	// suffix. Empty uses "shared-signal-collection".
	GroupNamePrefix string
}

func metricWatchName(prefix string, processID int, sequence uint64) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = defaultGroupNamePrefix
	}
	return fmt.Sprintf("%s-%d-%d", prefix, processID, sequence)
}

// Source owns one lazy DCGM client reference. go-dcgm serializes and
// reference-counts Init/Shutdown process-wide; each successful Init returns
// one balanced cleanup function retained here.
type Source struct {
	native nativeAPI
	// Production sources share this lock and fail fast when it is held. The lock
	// protects native calls and the source state changed by those calls.
	nativeCallMu       *sync.Mutex
	cleanup            func()
	watchCleanup       func() error
	processMode        *sessionMode
	metricWatchSignals []string
	metricWatchFrozen  atomic.Bool
	ready              bool
	closed             bool
	mode               sessionMode
}

// NewStandaloneSource creates a source connected to an external DCGM
// hostengine.
func NewStandaloneSource(options StandaloneOptions) *Source {
	if options.Address == "" {
		options.Address = "localhost"
	}
	source := newSource(newNative(options, standaloneMode), &defaultNativeCallMu)
	source.mode = standaloneMode
	source.processMode = &defaultProcessMode
	return source
}

// NewEmbeddedSource creates a source that starts DCGM inside this process on
// its first collection and shuts it down on Close. It never checks for another
// DCGM engine on the node; the caller must. It is meant for short-lived one-shot
// processes; long-running agents should use NewStandaloneSource. A process runs
// DCGM in one mode, so a source of the other mode reports NOT_CONFIGURED.
func NewEmbeddedSource(options EmbeddedOptions) *Source {
	source := newSource(newNative(StandaloneOptions{GroupNamePrefix: options.GroupNamePrefix}, embeddedMode), &defaultNativeCallMu)
	source.mode = embeddedMode
	source.processMode = &defaultProcessMode
	return source
}

func newSource(native nativeAPI, nativeCallMu *sync.Mutex) *Source {
	return &Source{native: native, nativeCallMu: nativeCallMu}
}

// ID identifies this source to sensors and observations.
func (*Source) ID() string { return Name }

// AddMetricsToWatch registers metrics before collection begins. Sensors may
// call it more than once during setup; duplicate signals are ignored. The
// first ReadGPUMetrics call freezes the set and creates the DCGM watch lazily.
//
// AddMetricsToWatch must not run concurrently with collection or Close.
func (source *Source) AddMetricsToWatch(signalIDs []string) error {
	if source.closed {
		return fmt.Errorf("DCGM source is closed")
	}
	if source.metricWatchFrozen.Load() || source.ready || source.watchCleanup != nil {
		return fmt.Errorf("DCGM source collection already started")
	}
	if len(signalIDs) == 0 {
		return fmt.Errorf("at least one DCGM metric signal is required")
	}

	seen := make(map[string]struct{}, len(source.metricWatchSignals)+len(signalIDs))
	for _, signalID := range source.metricWatchSignals {
		seen[signalID] = struct{}{}
	}
	registered := make([]string, 0, len(signalIDs))
	for _, signalID := range signalIDs {
		if strings.TrimSpace(signalID) == "" {
			return fmt.Errorf("DCGM metric signal is required")
		}
		if _, duplicate := seen[signalID]; duplicate {
			continue
		}
		seen[signalID] = struct{}{}
		registered = append(registered, signalID)
	}
	source.metricWatchSignals = append(source.metricWatchSignals, registered...)
	return nil
}

// startNativeCall enters DCGM without waiting. After it succeeds, the caller
// must defer finishNativeCall.
func (source *Source) startNativeCall() error {
	if !source.nativeCallMu.TryLock() {
		return ErrInUse
	}
	if source.closed {
		source.nativeCallMu.Unlock()
		return errClosed
	}
	return nil
}

// finishNativeCall releases the process-wide native-call lock.
func (source *Source) finishNativeCall() {
	source.nativeCallMu.Unlock()
}

// ensureInitialized opens the DCGM client on the first native operation.
func (source *Source) ensureInitialized() error {
	if source.ready {
		return nil
	}
	if source.processMode != nil && *source.processMode != 0 && *source.processMode != source.mode {
		return &dcgmError{
			category: observation.CollectionErrorCategoryNotConfigured,
			detail: fmt.Sprintf("this process set DCGM to %s mode with its first DCGM source and keeps that mode until it exits; this source uses %s mode",
				*source.processMode, source.mode),
		}
	}
	cleanup, err := source.native.Init()
	if err != nil {
		return err
	}
	if source.processMode != nil && *source.processMode == 0 {
		*source.processMode = source.mode
	}
	source.cleanup = cleanup
	source.ready = true
	return nil
}

// ensureMetricWatch creates the configured DCGM watch on its first call.
func (source *Source) ensureMetricWatch() error {
	if source.watchCleanup != nil {
		return nil
	}
	cleanup, err := source.native.WatchMetrics(source.metricWatchSignals)
	if err != nil {
		return err
	}
	if cleanup == nil {
		return fmt.Errorf("DCGM metric watch returned no cleanup function")
	}
	source.watchCleanup = cleanup
	return nil
}

// Close releases the watch and DCGM client. It returns ErrInUse instead of
// waiting when a native call may still be running.
func (source *Source) Close() error {
	if !source.nativeCallMu.TryLock() {
		return ErrInUse
	}
	defer source.nativeCallMu.Unlock()
	if source.closed {
		return nil
	}
	source.closed = true
	watchCleanup := source.watchCleanup
	source.watchCleanup = nil
	cleanup := source.cleanup
	source.cleanup = nil
	source.ready = false
	var err error
	if watchCleanup != nil {
		err = watchCleanup()
	}
	if cleanup != nil {
		cleanup()
	}
	return err
}
