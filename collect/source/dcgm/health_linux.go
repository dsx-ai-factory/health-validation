//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	ops               healthWatchOperations
	targets           map[godcgm.Field_Entity_Group][]uint
	monitoredSince    time.Time
	name              string
	group             godcgm.GroupHandle
	systems           uint64
	configuredSystems uint64
	owned             bool
	configured        bool
	closed            bool
}

var healthEntitySelections = [...]struct {
	systems uint64
	group   godcgm.Field_Entity_Group
}{
	{systems: dcgmhealth.GPUSystems, group: godcgm.FE_GPU},
	{systems: dcgmhealth.NVSwitchSystems, group: godcgm.FE_SWITCH},
}

const maxNativeHealthIncidents = 1024

// WatchHealth prepares a watch whose native setup occurs during Read.
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

// startHealthWatch validates health selection without allocating native resources.
func startHealthWatch(name string, systems uint64, ops healthWatchOperations) (healthWatch, error) {
	if systems == 0 || systems&^dcgmhealth.KnownSystems != 0 {
		return nil, fmt.Errorf("invalid DCGM health-system mask %#x", systems)
	}
	return &nativeHealthWatch{
		ops: ops, targets: make(map[godcgm.Field_Entity_Group][]uint), name: name, systems: systems,
	}, nil
}

// Read returns group membership and the complete native health response.
// A group without targets has no native overall result.
func (watch *nativeHealthWatch) Read() (*observation.DCGMHealthReport, error) {
	if watch.closed {
		return nil, errClosed
	}
	// A previous setup failed, and rollback could not destroy its group.
	// Finish that cleanup before creating a replacement.
	if watch.owned && !watch.configured {
		if err := watch.destroyOwnedGroup(); err != nil {
			return nil, err
		}
	}
	report, availableSystems, err := watch.discoverTargets()
	if err != nil {
		if !watch.owned {
			// Without an existing group, retry discovery from scratch next time.
			clear(watch.targets)
		}
		return nil, err
	}
	if err = watch.configureGroup(availableSystems); err != nil {
		return nil, err
	}
	if !watch.owned {
		// No available targets means no native check or overall health result.
		return report, nil
	}
	groupID := uint64(watch.group.GetHandle())
	report.GroupId = &groupID
	report.MonitoredSince = timestamppb.New(watch.monitoredSince)
	info, err := watch.ops.info(watch.group)
	if err != nil {
		return nil, fmt.Errorf("read DCGM health group: %w", dcgmErrorFromError(err))
	}
	if info == nil {
		return nil, fmt.Errorf("DCGM returned no health group information")
	}
	var presentSystems, unavailableSystems uint64
	for _, entity := range info.EntityList {
		report.Entities = append(report.Entities, healthEntity(entity))
		for _, selection := range healthEntitySelections {
			if entity.EntityGroupId == selection.group {
				presentSystems |= watch.systems & selection.systems
			}
		}
	}
	for system := range report.SystemErrors {
		unavailableSystems |= system
	}
	// Failed discovery leaves availability unknown; it does not mean no targets.
	report.SystemsWithoutTargets = watch.systems &^ (presentSystems | unavailableSystems)
	if len(info.EntityList) == 0 {
		return report, nil
	}
	response, err := watch.ops.check(watch.group)
	if err != nil {
		return nil, fmt.Errorf("check DCGM health: %w", dcgmErrorFromError(err))
	}
	nativeHealth := observation.DCGMHealthResult(response.OverallHealth)
	report.NativeGroupOverallHealth = &nativeHealth
	// A full native response may omit more incidents without reporting their count.
	report.PossiblyTruncated = len(response.Incidents) >= maxNativeHealthIncidents
	for _, incident := range response.Incidents {
		report.Incidents = append(report.Incidents, &observation.DCGMHealthIncident{
			System:       uint64(incident.System),
			NativeHealth: observation.DCGMHealthResult(incident.Health).Enum(),
			ErrorCode:    uint64(incident.Error.Code),
			Message:      incident.Error.Message,
			Entity:       healthEntity(incident.EntityInfo),
		})
	}
	return report, nil
}

// discoverTargets retries unavailable or empty entity kinds, retaining known targets.
func (watch *nativeHealthWatch) discoverTargets() (*observation.DCGMHealthReport, uint64, error) {
	report := &observation.DCGMHealthReport{RequestedSystems: watch.systems}
	var availableSystems, knownSystems uint64
	var failures []error
	for _, selection := range healthEntitySelections {
		selected := watch.systems & selection.systems
		if selected == 0 {
			continue
		}
		// Reuse nonempty discoveries. Retry failed or empty kinds so they
		// can join the group when they become available.
		ids := watch.targets[selection.group]
		if len(ids) == 0 {
			var err error
			ids, err = watch.ops.entities(selection.group)
			if err != nil {
				failure := fmt.Errorf("enumerate DCGM health entity kind %d: %w", selection.group, dcgmErrorFromError(err))
				// Connection loss affects every kind and needs session recovery.
				if errors.Is(failure, errConnectionNotValid) {
					return nil, 0, failure
				}
				failures = append(failures, failure)
				if report.SystemErrors == nil {
					report.SystemErrors = make(map[uint64]*observation.CollectionError)
				}
				// Every requested subsystem for this kind shares its discovery failure.
				for system := uint64(1); system != 0; system <<= 1 {
					if selected&system != 0 {
						detail := collectionError(failure)
						detail.Source, detail.Operation = Name, "health discovery"
						report.SystemErrors[system] = &detail
					}
				}
				continue
			}
			watch.targets[selection.group] = slices.Clone(ids)
		}
		// A successful empty lookup still establishes that this kind has no targets.
		knownSystems |= selected
		if len(ids) == 0 {
			report.SystemsWithoutTargets |= selected
		} else {
			availableSystems |= selected
		}
	}
	// If every requested kind failed discovery, there is no partial report.
	if knownSystems == 0 && len(failures) > 0 {
		return nil, 0, errors.Join(failures...)
	}
	return report, availableSystems, nil
}

// configureGroup adds newly discovered targets before updating the enabled systems.
func (watch *nativeHealthWatch) configureGroup(systems uint64) error {
	if systems == 0 || (watch.configured && watch.configuredSystems == systems) {
		return nil
	}
	if !watch.owned {
		group, err := watch.ops.create(watch.name)
		if err != nil {
			clear(watch.targets)
			return fmt.Errorf("create DCGM health group: %w", dcgmErrorFromError(err))
		}
		watch.group, watch.owned = group, true
	}
	// Add only newly available kinds to the same group; configured kinds
	// already have their members and must not be added again.
	for _, selection := range healthEntitySelections {
		if systems&selection.systems == 0 || watch.configuredSystems&selection.systems != 0 {
			continue
		}
		for _, id := range watch.targets[selection.group] {
			if err := watch.ops.add(watch.group, selection.group, id); err != nil {
				return watch.rollbackSetup(fmt.Errorf("add entity to DCGM health group: %w", dcgmErrorFromError(err)))
			}
		}
	}
	if err := watch.ops.set(watch.group, godcgm.HealthSystem(systems)); err != nil {
		return watch.rollbackSetup(fmt.Errorf("enable DCGM health systems: %w", dcgmErrorFromError(err)))
	}
	// Extending this group keeps its original start time; new systems may
	// have less history than systems enabled during the first setup.
	if !watch.configured {
		watch.monitoredSince = time.Now()
	}
	watch.configuredSystems, watch.configured = systems, true
	return nil
}

// rollbackSetup retains a group until native destruction succeeds.
func (watch *nativeHealthWatch) rollbackSetup(cause error) error {
	// AddEntity or HealthSet may have partially changed an existing group.
	// Stop trusting the whole configuration until rollback finishes.
	watch.configured = false
	return errors.Join(cause, watch.destroyOwnedGroup())
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
	// Close is terminal; failed destruction is retried only during setup rollback.
	watch.closed = true
	return watch.destroyOwnedGroup()
}

// destroyOwnedGroup clears ownership only after successful destruction.
func (watch *nativeHealthWatch) destroyOwnedGroup() error {
	if !watch.owned {
		return nil
	}
	if err := watch.ops.destroy(watch.group); err != nil {
		return fmt.Errorf("destroy DCGM health group: %w", dcgmErrorFromError(err))
	}
	watch.owned, watch.configured = false, false
	watch.configuredSystems = 0
	watch.monitoredSince = time.Time{}
	clear(watch.targets)
	return nil
}
