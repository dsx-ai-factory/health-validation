// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

const gpuEntityKind = 1

// AddHealthToWatch adds systems to the requested union for lazy watch setup.
// Zero is a no-op; other masks must contain only dcgmhealth.KnownSystems bits.
// The first metric or health read freezes registration; inventory reads leave
// it open. Targets are discovered and watches are created lazily.
// It must not run concurrently with collection or Close.
func (source *Source) AddHealthToWatch(systems uint64) error {
	if source.closed.Load() {
		return errClosed
	}
	if source.configurationError != nil {
		return source.configurationError
	}
	if source.registrationFrozen() {
		return fmt.Errorf("DCGM source collection already started")
	}
	if systems&^dcgmhealth.KnownSystems != 0 {
		return fmt.Errorf("invalid DCGM health-system mask %#x", systems)
	}
	if systems != 0 && source.healthConfigurationError != nil {
		return source.healthConfigurationError
	}
	source.healthSystems |= systems
	return nil
}

// readHealth collects native findings and optionally enriches GPU identities.
func (reader *collectionReader) readHealth(ctx context.Context) (*observation.DCGMHealthReport, *observation.CollectionError) {
	report, failure := reader.readHealthReport(ctx)
	if failure != nil {
		return nil, failure
	}
	// Identity lookup runs last so its failure cannot discard health findings.
	reader.enrichHealthIdentities(ctx, report)
	return report, nil
}

// readHealthReport assembles the requested group outcomes into one report.
func (reader *collectionReader) readHealthReport(ctx context.Context) (*observation.DCGMHealthReport, *observation.CollectionError) {
	source := reader.source
	source.watchRegistrationFrozen.Store(true)
	if source.healthSystems == 0 {
		return nil, &observation.CollectionError{
			Source: Name, Operation: "health",
			Category: observation.CollectionErrorCategoryNotConfigured,
			Detail:   "no DCGM health systems are registered",
		}
	}
	report := &observation.DCGMHealthReport{RequestedSystems: source.healthSystems, CollectionId: reader.cycle.CollectionID()}
	var failures []error
	hasReport := false
	// Refresh GPU targets and clean up missing GPUs' groups before reading health.
	if systems := source.healthSystems & dcgmhealth.GPUSystems; systems != 0 {
		discovery, discoveryError := reader.discoverHealthGPUs(ctx)
		// Successful empty discovery is reportable; known GPUs can carry setup errors.
		hasReport = discoveryError == nil || len(discovery.ids) != 0
		withoutTargets := systems
		if discoveryError != nil {
			// Availability is unknown. Report discovery errors while keeping any
			// findings obtainable from cached GPU watches.
			withoutTargets = 0
			failure := collectionError(discoveryError)
			failure.Source, failure.Operation = Name, "health discovery"
			report.SystemErrors = make(map[uint64]*observation.CollectionError)
			for system := uint64(1); system != 0; system <<= 1 {
				if systems&system != 0 {
					report.SystemErrors[system] = proto.Clone(&failure).(*observation.CollectionError)
				}
			}
			failures = append(failures, discoveryError)
		}

		// Keep each GPU's setup or check failure separate from other GPUs' findings.
		for _, gpuID := range discovery.ids {
			partial, err := reader.readHealthGroup(ctx, systems, &gpuID)
			group := appendHealthGroup(report, partial, systems, &gpuID, err)
			// Every GPU group must confirm absence before the system is target-free.
			if partial == nil || group.GetCollectionError() != nil {
				withoutTargets = 0
			} else {
				withoutTargets &= partial.systemsWithoutTargets
			}
		}

		// Keep errors for GPUs no longer in the read list. If all targets are absent,
		// NO_TARGETS would otherwise hide failed cleanup of their native groups.
		for _, gpuID := range slices.Sorted(maps.Keys(discovery.cleanupErrors)) {
			err := discovery.cleanupErrors[gpuID]
			appendHealthGroup(report, nil, systems, &gpuID, err)
			withoutTargets = 0
		}
		report.SystemsWithoutTargets |= withoutTargets
	}

	// Switch systems share one group; retain their outcome alongside the GPU results.
	if systems := source.healthSystems & dcgmhealth.NVSwitchSystems; systems != 0 {
		partial, err := reader.readHealthGroup(ctx, systems, nil)
		appendHealthGroup(report, partial, systems, nil, err)
		if partial != nil {
			hasReport = true
			report.SystemsWithoutTargets |= partial.systemsWithoutTargets
		}
		if err != nil {
			failures = append(failures, err)
		}
	}

	// Use a source-wide error only when there is no usable discovery or group outcome
	// to describe. Otherwise, return the report with its scoped failures.
	if !hasReport {
		failure := collectionError(errors.Join(failures...))
		failure.Source, failure.Operation = Name, "health"
		return nil, &failure
	}
	return report, nil
}

// healthGPUDiscovery carries discovered IDs and failed cleanup outcomes out of
// the timed worker, which must not mutate the caller's report after a timeout.
type healthGPUDiscovery struct {
	// Cleanup failures by GPU ID; the session retains those watches for retry.
	cleanupErrors map[uint]error
	ids           []uint
}

// discoverHealthGPUs discovers active IDs and reconciles cached GPU watches.
func (reader *collectionReader) discoverHealthGPUs(ctx context.Context) (healthGPUDiscovery, error) {
	source := reader.source
	return runNative(ctx, reader, func(callContext context.Context, session *clientSession) (healthGPUDiscovery, error) {
		result := healthGPUDiscovery{}
		ids, err := source.native.HealthGPUIndices()
		if err != nil {
			// Failed discovery is not evidence that a GPU disappeared. Keep the
			// existing watches and try reading their known targets instead.
			result.ids = slices.Sorted(maps.Keys(session.gpuHealthWatches))
			return result, err
		}

		// Deduplicate targets while preserving discovery order for the group reads.
		present := make(map[uint]bool, len(ids))
		for _, id := range ids {
			if !present[id] {
				result.ids = append(result.ids, id)
				present[id] = true
			}
		}

		// Queue missing GPUs' watches only after successful discovery. Retain
		// ownership outside the read cache until group destruction succeeds.
		for id, watch := range session.gpuHealthWatches {
			if !present[id] {
				if session.pendingGPUHealthCleanup == nil {
					session.pendingGPUHealthCleanup = make(map[uint]healthWatch)
				}
				session.pendingGPUHealthCleanup[id] = watch
				delete(session.gpuHealthWatches, id)
			}
		}

		// Clean up newly queued watches and retry older failures before new
		// groups can consume more of the hostengine's shared group budget.
		for _, id := range slices.Sorted(maps.Keys(session.pendingGPUHealthCleanup)) {
			if contextErr := callContext.Err(); contextErr != nil {
				return result, contextErr
			}
			if err := session.pendingGPUHealthCleanup[id].Close(); err != nil {
				// Leave the watch in pending cleanup so ownership and retries survive.
				if result.cleanupErrors == nil {
					result.cleanupErrors = make(map[uint]error)
				}
				result.cleanupErrors[id] = err
				if errors.Is(err, errConnectionNotValid) {
					// Stop using this session; later steps must fail until a fresh
					// collection recovers the connection.
					session.recordConnectionFailure(reader.cycle, err)
					break
				}
			} else {
				delete(session.pendingGPUHealthCleanup, id)
			}
		}

		// Hold back returning GPUs whose old groups still need cleanup. Other IDs
		// proceed to readHealthGroup, which reuses or lazily creates their watches.
		result.ids = slices.DeleteFunc(result.ids, func(id uint) bool { return result.cleanupErrors[id] != nil })
		return result, nil
	})
}

// appendHealthGroup appends findings and scoped errors to the aggregate report.
func appendHealthGroup(report *observation.DCGMHealthReport, partial *healthGroupRead, systems uint64, gpuID *uint, err error) *observation.DCGMHealthGroupResult {
	kind := uint32(3)
	if gpuID != nil {
		kind = gpuEntityKind
	}
	var group *observation.DCGMHealthGroupResult
	if partial != nil {
		// Preserve partial findings and discovery errors even if the read also failed.
		group = partial.group
		if len(partial.systemErrors) != 0 && report.SystemErrors == nil {
			report.SystemErrors = make(map[uint64]*observation.CollectionError)
		}
		for system, failure := range partial.systemErrors {
			report.SystemErrors[system] = failure
		}
	} else {
		group = &observation.DCGMHealthGroupResult{}
	}
	group.EntityKind, group.Systems = kind, systems
	if err != nil {
		failure := collectionError(err)
		failure.Source, failure.Operation = Name, "health"
		group.CollectionError = &failure
		if gpuID != nil && len(group.EntityResults) == 0 {
			// Setup may fail before membership is verified. Identify the GPU
			// without inventing membership, monitoring history, or native health.
			group.EntityResults = []*observation.DCGMHealthEntityResult{{Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{
				Native: &observation.NativeEntityRef{Source: Name, EntityKind: gpuEntityKind, EntityId: uint32(*gpuID)},
			}}}}}
		}
	}
	report.GroupResults = append(report.GroupResults, group)
	return group
}

// readHealthGroup reads and snapshots one group's outcome on the pinned session.
func (reader *collectionReader) readHealthGroup(ctx context.Context, systems uint64, gpuID *uint) (*healthGroupRead, error) {
	source := reader.source
	return runNative(ctx, reader, func(callContext context.Context, session *clientSession) (*healthGroupRead, error) {
		// Reuse a watch or prepare one; native group setup happens lazily in Read.
		watch, err := source.ensureHealthWatch(session, systems, gpuID)
		if err != nil {
			return nil, err
		}
		if source.closed.Load() {
			return nil, errClosed
		}
		if contextErr := callContext.Err(); contextErr != nil {
			return nil, contextErr
		}

		// Setup and checking share one timed native step, which can return
		// partial membership and findings alongside an error.
		partial, err := watch.Read()
		if partial == nil || partial.group == nil {
			if err != nil {
				return nil, err
			}
			return nil, &dcgmError{
				category: observation.CollectionErrorCategorySourceError,
				detail:   "DCGM health check returned no report",
			}
		}

		// Clone under the gate so later enrichment cannot mutate native data
		// that another interleaved collection may reuse.
		group := proto.Clone(partial.group).(*observation.DCGMHealthGroupResult)
		// Connection loss can be embedded in results. Record it before releasing
		// the gate so later steps cannot mix old findings with new GPU identities.
		session.recordFieldFailure(reader.cycle, group.GetCollectionError())
		for _, result := range group.GetEntityResults() {
			for _, failure := range result.GetCollectionErrors() {
				session.recordFieldFailure(reader.cycle, failure.GetError())
			}
		}
		discoveryErrors := maps.Clone(partial.systemErrors)
		for system, failure := range discoveryErrors {
			discoveryErrors[system] = proto.Clone(failure).(*observation.CollectionError)
			session.recordFieldFailure(reader.cycle, discoveryErrors[system])
		}
		return &healthGroupRead{group: group, systemErrors: discoveryErrors, systemsWithoutTargets: partial.systemsWithoutTargets}, err
	})
}

// enrichHealthIdentities adds optional GPU UUIDs and PCI addresses to the report.
func (reader *collectionReader) enrichHealthIdentities(ctx context.Context, report *observation.DCGMHealthReport) {
	var gpuIDs []uint
	seen := make(map[uint32]bool)
	// Include GPUs mentioned only by errors, deduplicating IDs across groups.
	for _, group := range report.GroupResults {
		for _, result := range group.GetEntityResults() {
			native := result.GetEntity().GetGpu().GetNative()
			if native.GetSource() == Name && native.GetEntityKind() == gpuEntityKind && !seen[native.GetEntityId()] {
				seen[native.GetEntityId()] = true
				gpuIDs = append(gpuIDs, uint(native.GetEntityId()))
			}
		}
	}
	if len(gpuIDs) == 0 {
		return
	}

	// One logical lookup serves every matching entity; missing identities leave
	// the native IDs and health findings intact.
	identities := reader.gpuUUIDs(ctx, gpuIDs)
	byIndex := make(map[uint]GPUIdentityEntry, len(identities))
	for _, identity := range identities {
		byIndex[identity.Index] = identity
	}
	for _, group := range report.GroupResults {
		for _, result := range group.GetEntityResults() {
			gpu := result.GetEntity().GetGpu()
			native := gpu.GetNative()
			if native.GetSource() == Name && native.GetEntityKind() == gpuEntityKind {
				identity := byIndex[uint(native.GetEntityId())]
				if identity.UUID != "" {
					gpu.Uuid = identity.UUID
				}
				if identity.BusID != "" {
					gpu.BusId = identity.BusID
				}
			}
		}
	}
}

// ensureHealthWatch returns or constructs a session-owned watch after cleanup.
func (source *Source) ensureHealthWatch(session *clientSession, systems uint64, gpuID *uint) (healthWatch, error) {
	if source.closed.Load() {
		return nil, errClosed
	}
	if source.healthConfigurationError != nil {
		return nil, source.healthConfigurationError
	}
	if gpuID != nil {
		// Another collection may queue this GPU's watch for cleanup after discovery.
		// Finish its old group's cleanup before allowing a replacement watch.
		if pending := session.pendingGPUHealthCleanup[*gpuID]; pending != nil {
			if err := pending.Close(); err != nil {
				return nil, fmt.Errorf("clean up GPU %d health watch: %w", *gpuID, err)
			}
			delete(session.pendingGPUHealthCleanup, *gpuID)
		}
	}

	// Reuse cached watches to retain monitoring history and pending setup state.
	var watch healthWatch
	if gpuID == nil {
		watch = session.switchHealthWatch
	} else {
		watch = session.gpuHealthWatches[*gpuID]
	}
	if watch != nil {
		return watch, nil
	}

	// Cache the watch object now; Read allocates/configures its group when targets
	// are available.
	watch, err := source.native.WatchHealth(systems, gpuID)
	if err == nil && watch == nil {
		err = fmt.Errorf("DCGM health watch returned no watch")
	}
	if err != nil {
		return nil, err
	}
	if gpuID == nil {
		session.switchHealthWatch = watch
	} else {
		if session.gpuHealthWatches == nil {
			session.gpuHealthWatches = make(map[uint]healthWatch)
		}
		session.gpuHealthWatches[*gpuID] = watch
	}
	return watch, nil
}
