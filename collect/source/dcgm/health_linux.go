//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"os"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

type healthWatchOperations struct {
	entities func(godcgm.Field_Entity_Group) ([]uint, error)
	create   func(string) (godcgm.GroupHandle, error)
	add      func(godcgm.GroupHandle, godcgm.Field_Entity_Group, uint) error
	set      func(godcgm.GroupHandle, godcgm.HealthSystem) error
	info     func(godcgm.GroupHandle) (*godcgm.GroupInfo, error)
	check    func(godcgm.GroupHandle) (godcgm.HealthResponse, error)
	destroy  func(godcgm.GroupHandle) error
}

type nativeHealthWatch struct {
	ops     healthWatchOperations
	group   godcgm.GroupHandle
	systems uint64
	owned   bool
	closed  bool
}

// WatchHealth uses a uniquely named owned group when requested targets exist.
func (library *nativeLibrary) WatchHealth(systems uint64) (healthWatch, error) {
	name := metricWatchName(library.options.GroupNamePrefix, os.Getpid(), metricWatchSequence.Add(1)) + "-health"
	return startHealthWatch(name, systems, healthWatchOperations{
		entities: godcgm.GetEntityGroupEntities,
		create:   godcgm.CreateGroup,
		add:      godcgm.AddEntityToGroup,
		set:      godcgm.HealthSet,
		info:     godcgm.GetGroupInfo,
		check:    godcgm.HealthCheck,
		destroy:  godcgm.DestroyGroup,
	})
}

// startHealthWatch configures a health group for discovered targets, if any.
// After group creation, setup failure attempts destruction once.
func startHealthWatch(name string, systems uint64, ops healthWatchOperations) (healthWatch, error) {
	if systems == 0 || systems&^dcgmhealth.KnownSystems != 0 {
		return nil, fmt.Errorf("invalid DCGM health-system mask %#x", systems)
	}
	var entities []godcgm.GroupEntityPair
	for _, selection := range []struct {
		systems uint64
		group   godcgm.Field_Entity_Group
	}{
		{systems: dcgmhealth.GPUSystems, group: godcgm.FE_GPU},
		{systems: dcgmhealth.NVSwitchSystems, group: godcgm.FE_SWITCH},
	} {
		if systems&selection.systems == 0 {
			continue
		}
		ids, err := ops.entities(selection.group)
		if err != nil {
			return nil, fmt.Errorf("enumerate DCGM health entities: %w", dcgmErrorFromError(err))
		}
		for _, id := range ids {
			entities = append(entities, godcgm.GroupEntityPair{EntityGroupId: selection.group, EntityId: id})
		}
	}
	watch := &nativeHealthWatch{ops: ops, systems: systems}
	if len(entities) == 0 {
		return watch, nil
	}
	group, err := ops.create(name)
	if err != nil {
		return nil, fmt.Errorf("create DCGM health group: %w", dcgmErrorFromError(err))
	}
	watch.group, watch.owned = group, true
	rollback := func(cause error) (healthWatch, error) {
		return nil, errors.Join(dcgmErrorFromError(cause), watch.Close())
	}
	for _, entity := range entities {
		if err := ops.add(group, entity.EntityGroupId, entity.EntityId); err != nil {
			return rollback(fmt.Errorf("add entity to DCGM health group: %w", err))
		}
	}
	if err := ops.set(group, godcgm.HealthSystem(systems)); err != nil {
		return rollback(fmt.Errorf("enable DCGM health systems: %w", err))
	}
	return watch, nil
}

// Read returns group membership and the complete native health response.
// A group without targets has no native overall result.
func (watch *nativeHealthWatch) Read() (*observation.DCGMHealthReport, error) {
	if watch.closed {
		return nil, errClosed
	}
	report := &observation.DCGMHealthReport{RequestedSystems: watch.systems}
	if !watch.owned {
		return report, nil
	}
	groupID := uint64(watch.group.GetHandle())
	report.GroupId = &groupID
	info, err := watch.ops.info(watch.group)
	if err != nil {
		return nil, fmt.Errorf("read DCGM health group: %w", dcgmErrorFromError(err))
	}
	if info == nil {
		return nil, fmt.Errorf("DCGM returned no health group information")
	}
	if len(info.EntityList) == 0 {
		return report, nil
	}
	response, err := watch.ops.check(watch.group)
	if err != nil {
		return nil, fmt.Errorf("check DCGM health: %w", dcgmErrorFromError(err))
	}
	nativeHealth := observation.DCGMHealthResult(response.OverallHealth)
	report.NativeGroupOverallHealth = &nativeHealth
	for _, entity := range info.EntityList {
		report.Entities = append(report.Entities, healthEntity(entity))
	}
	for _, incident := range response.Incidents {
		report.Incidents = append(report.Incidents, &observation.DCGMHealthIncident{
			System:       uint64(incident.System),
			NativeHealth: observation.DCGMHealthResult(incident.Health),
			ErrorCode:    uint64(incident.Error.Code),
			Message:      incident.Error.Message,
			Entity:       healthEntity(incident.EntityInfo),
		})
	}
	return report, nil
}

// healthEntity maps native pairs to shared targets, retaining unknown kinds
// and source-scoped numeric identities.
func healthEntity(entity godcgm.GroupEntityPair) *observation.ObservationTarget {
	native := &observation.NativeEntityRef{
		Source:     Name,
		EntityKind: uint32(entity.EntityGroupId),
		EntityId:   uint32(entity.EntityId),
	}
	switch entity.EntityGroupId {
	case godcgm.FE_GPU:
		return &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{Native: native}}}
	case godcgm.FE_SWITCH:
		return &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: native}}}
	default:
		return &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: native}}
	}
}

// Close attempts owned group destruction once. Native health sampling may
// persist in an external hostengine even after the client disconnects.
func (watch *nativeHealthWatch) Close() error {
	if watch.closed {
		return nil
	}
	watch.closed = true
	if !watch.owned {
		return nil
	}
	watch.owned = false
	if err := watch.ops.destroy(watch.group); err != nil {
		return fmt.Errorf("destroy DCGM health group: %w", dcgmErrorFromError(err))
	}
	return nil
}
