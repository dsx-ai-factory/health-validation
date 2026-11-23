// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
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

// readHealth keeps the native report if optional identity lookup fails.
func (reader *collectionReader) readHealth(ctx context.Context) (*observation.DCGMHealthReport, *observation.CollectionError) {
	report, failure := reader.readHealthReport(ctx)
	if failure != nil {
		return nil, failure
	}
	reader.enrichHealthIdentities(ctx, report)
	return report, nil
}

// readHealthReport clones the watch's report before releasing the native gate.
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
	report, err := runNative(ctx, reader, func(callContext context.Context, session *clientSession) (*observation.DCGMHealthReport, error) {
		if err := source.ensureHealthWatch(session); err != nil {
			return nil, err
		}
		if source.closed.Load() {
			return nil, errClosed
		}
		if err := callContext.Err(); err != nil {
			return nil, err
		}

		report, err := session.healthWatch.Read()
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
		report.CollectionId = reader.cycle.CollectionID()
		for _, failure := range report.SystemErrors {
			session.recordFieldFailure(reader.cycle, failure)
		}
		return report, nil
	})
	if err != nil {
		failure := collectionError(err)
		failure.Source = Name
		failure.Operation = "health"
		return nil, &failure
	}
	return report, nil
}

// enrichHealthIdentities adds available UUIDs and PCI addresses without discarding the report on
// lookup failure. Its separate native call uses the shared timeout gate.
func (reader *collectionReader) enrichHealthIdentities(ctx context.Context, report *observation.DCGMHealthReport) {
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
	identities := reader.gpuUUIDs(ctx, gpuIDs)
	byIndex := make(map[uint]GPUIdentityEntry, len(identities))
	for _, identity := range identities {
		byIndex[identity.Index] = identity
	}
	for _, entity := range entities {
		gpu := entity.GetGpu()
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

// ensureHealthWatch retains the native watch that owns setup and rollback state.
// Failed construction owns no resources and may retry on the next read.
func (source *Source) ensureHealthWatch(session *clientSession) error {
	if source.closed.Load() {
		return errClosed
	}
	if source.healthConfigurationError != nil {
		return source.healthConfigurationError
	}
	if session.healthWatch != nil {
		return nil
	}
	watch, err := source.native.WatchHealth(source.healthSystems)
	if err == nil && watch == nil {
		err = fmt.Errorf("DCGM health watch returned no watch")
	}
	if err != nil {
		return err
	}
	session.healthWatch = watch
	return nil
}
