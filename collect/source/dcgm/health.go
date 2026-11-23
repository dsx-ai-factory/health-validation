// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/collect"
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
	source.healthSystems |= systems
	return nil
}

// ReadHealth performs a fresh health read with lazy setup and optional GPU UUID
// enrichment. Sensors call it once when health is selected for Measure.
// Success returns a nonnil report whose CollectionId is the cycle's collection ID.
// Successful reports with no incidents are returned immediately.
// No registered systems returns NotConfigured without native setup.
// Runtime setup failures retry on a later read, retaining unfinished cleanup.
// Connection loss requires a fresh session in a later collection cycle.
func (source *Source) ReadHealth(ctx context.Context, currentCycle *collect.Cycle) (*observation.DCGMHealthReport, *observation.CollectionError) {
	source.watchRegistrationFrozen.Store(true)
	if source.healthSystems == 0 {
		return nil, &observation.CollectionError{
			Source: Name, Operation: "health",
			Category: observation.CollectionErrorCategoryNotConfigured,
			Detail:   "no DCGM health systems are registered",
		}
	}
	report, err := runNative(ctx, currentCycle, source, func(callContext context.Context) (*observation.DCGMHealthReport, error) {
		if err := source.ensureHealthWatch(); err != nil {
			return nil, err
		}
		if source.closed.Load() {
			return nil, errClosed
		}
		if err := callContext.Err(); err != nil {
			return nil, err
		}

		report, err := source.healthWatch.Read()
		if err != nil {
			return nil, err
		}
		if report == nil {
			return nil, &dcgmError{
				category: observation.CollectionErrorCategorySourceError,
				detail:   "DCGM health check returned no report",
			}
		}
		report = proto.Clone(report).(*observation.DCGMHealthReport)
		report.RequestedSystems = source.healthSystems
		report.CollectionId = currentCycle.CollectionID()
		for _, failure := range report.SystemErrors {
			source.recordFieldFailure(currentCycle, failure)
		}
		return report, nil
	})
	if err != nil {
		failure := collectionError(err)
		failure.Source = Name
		failure.Operation = "health"
		return nil, &failure
	}
	source.enrichHealthUUIDs(ctx, currentCycle, report)
	return report, nil
}

// enrichHealthUUIDs adds available GPU UUIDs without discarding the report on
// lookup failure. Its separate native call uses the shared timeout gate.
func (source *Source) enrichHealthUUIDs(ctx context.Context, currentCycle *collect.Cycle, report *observation.DCGMHealthReport) {
	entities := slices.Clone(report.Entities)
	for _, incident := range report.Incidents {
		entities = append(entities, incident.GetEntity())
	}
	var gpuIDs []uint
	seen := make(map[uint32]bool)
	for _, entity := range entities {
		native := entity.GetGpu().GetNative()
		if native.GetSource() == Name && native.GetEntityKind() == gpuEntityKind && !seen[native.GetEntityId()] {
			seen[native.GetEntityId()] = true
			gpuIDs = append(gpuIDs, uint(native.GetEntityId()))
		}
	}
	if len(gpuIDs) == 0 {
		return
	}
	identities := source.GPUUUIDs(ctx, currentCycle, gpuIDs)
	uuids := make(map[uint]string, len(identities))
	for _, identity := range identities {
		uuids[identity.Index] = identity.UUID
	}
	for _, entity := range entities {
		gpu := entity.GetGpu()
		native := gpu.GetNative()
		if native.GetSource() == Name && native.GetEntityKind() == gpuEntityKind {
			if uuid := uuids[uint(native.GetEntityId())]; uuid != "" {
				gpu.Uuid = uuid
			}
		}
	}
}

// ensureHealthWatch retains the native watch that owns setup and rollback state.
// Failed construction owns no resources and may retry on the next read.
func (source *Source) ensureHealthWatch() error {
	if source.closed.Load() {
		return errClosed
	}
	if source.healthWatch != nil {
		return nil
	}
	watch, err := source.native.WatchHealth(source.healthSystems)
	if err == nil && watch == nil {
		err = fmt.Errorf("DCGM health watch returned no watch")
	}
	if err != nil {
		return err
	}
	source.healthWatch = watch
	return nil
}
