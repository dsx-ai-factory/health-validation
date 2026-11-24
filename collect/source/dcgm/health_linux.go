//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"os"
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
	monitoredSince    time.Time
	name              string
	group             godcgm.GroupHandle
	systems           uint64
	configuredSystems uint64
	ownsGroup         bool
	configured        bool
	closed            bool
}

var healthEntitySelections = [...]struct {
	systems    uint64
	entityKind godcgm.Field_Entity_Group
}{
	{systems: dcgmhealth.GPUSystems, entityKind: godcgm.FE_GPU},
	{systems: dcgmhealth.NVSwitchSystems, entityKind: godcgm.FE_SWITCH},
}

const maxNativeHealthIncidents = 1024

// WatchHealth prepares a watch whose native setup occurs during Read.
func (library *nativeLibrary) WatchHealth(systems uint64) (healthWatch, error) {
	options, err := freezeHealthWatchOptions(library.options.HealthWatch)
	if err != nil {
		return nil, err
	}
	name := metricWatchName(library.options.GroupNamePrefix, os.Getpid(), metricWatchSequence.Add(1)) + "-health"
	return startHealthWatch(name, systems, healthWatchOperations{
		entities: func(kind godcgm.Field_Entity_Group) ([]uint, error) {
			if kind == godcgm.FE_GPU {
				// General enumeration includes detached GPUs, which reject watches.
				return library.healthGPUIndices()
			}
			return godcgm.GetEntityGroupEntities(kind)
		},
		create: godcgm.CreateGroup,
		add:    godcgm.AddEntityToGroup,
		set: func(group godcgm.GroupHandle, systems godcgm.HealthSystem) error {
			return library.healthSet(group, systems, options.UpdateInterval, options.MaxKeepAge)
		},
		info:    godcgm.GetGroupInfo,
		check:   godcgm.HealthCheck,
		destroy: godcgm.DestroyGroup,
	})
}

// startHealthWatch validates health selection without allocating native resources.
func startHealthWatch(name string, systems uint64, ops healthWatchOperations) (healthWatch, error) {
	if systems == 0 || systems&^dcgmhealth.KnownSystems != 0 {
		return nil, fmt.Errorf("invalid DCGM health-system mask %#x", systems)
	}
	return &nativeHealthWatch{
		ops: ops, name: name, systems: systems,
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
	if watch.ownsGroup && !watch.configured {
		if err := watch.destroyOwnedGroup(); err != nil {
			return nil, err
		}
	}
	report := &observation.DCGMHealthReport{RequestedSystems: watch.systems}
	targets, discoveryErr := watch.discoverTargets(report)
	if errors.Is(discoveryErr, errConnectionNotValid) {
		return nil, discoveryErr
	}
	var members []godcgm.GroupEntityPair
	var err error
	if watch.ownsGroup {
		members, err = watch.groupMembers()
		if err != nil {
			if code, ok := dcgmErrorCode(err); ok && code == godcgm.DCGM_ST_NOT_CONFIGURED {
				return nil, watch.rollbackSetup(err)
			}
			return nil, err
		}
	}
	// Failed discovery cannot invalidate known members. Without any successful
	// discovery or existing members, there is no usable health coverage to read.
	if len(targets) == 0 && len(members) == 0 && discoveryErr != nil {
		return nil, discoveryErr
	}
	results := make(map[godcgm.GroupEntityPair]*observation.DCGMHealthEntityResult)
	members, err = watch.configureGroup(targets, members, report, results)
	if err != nil {
		return nil, err
	}
	// Discovery or membership errors leave coverage incomplete, not target-free.
	report.SystemsWithoutTargets = watch.systems &^ healthSystemsForEntities(watch.systems, members)
	for system := range report.SystemErrors {
		report.SystemsWithoutTargets &^= system
	}
	for _, result := range report.EntityResults {
		for _, failure := range result.CollectionErrors {
			report.SystemsWithoutTargets &^= failure.GetSystems()
		}
	}
	for _, entity := range members {
		member := true
		healthEntityResult(report, results, entity).GroupMember = &member
	}
	if !watch.ownsGroup {
		// No available targets means no native check or overall health result.
		return report, nil
	}
	groupID := uint64(watch.group.GetHandle())
	report.GroupId = &groupID
	report.MonitoredSince = timestamppb.New(watch.monitoredSince)
	if len(members) == 0 {
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
		finding := &observation.DCGMHealthIncident{
			System:       uint64(incident.System),
			NativeHealth: observation.DCGMHealthResult(incident.Health).Enum(),
			ErrorCode:    uint64(incident.Error.Code),
			Message:      incident.Error.Message,
		}
		result := healthEntityResult(report, results, incident.EntityInfo)
		result.Incidents = append(result.Incidents, finding)
	}
	return report, nil
}

// discoverTargets refreshes every requested kind so returning devices can rejoin.
// Successful empty discoveries remain in targets; failures are also added to report.
func (watch *nativeHealthWatch) discoverTargets(report *observation.DCGMHealthReport) (map[godcgm.Field_Entity_Group][]uint, error) {
	targets := make(map[godcgm.Field_Entity_Group][]uint, len(healthEntitySelections))
	var failures []error
	for _, selection := range healthEntitySelections {
		selected := watch.systems & selection.systems
		if selected == 0 {
			continue
		}
		ids, err := watch.ops.entities(selection.entityKind)
		if err != nil {
			failure := fmt.Errorf("enumerate DCGM health entity kind %d: %w", selection.entityKind, dcgmErrorFromError(err))
			// Connection loss affects every kind and needs session recovery.
			if errors.Is(failure, errConnectionNotValid) {
				return nil, failure
			}
			failures = append(failures, failure)
			recordHealthDiscoveryError(report, selected, failure)
			continue
		}
		targets[selection.entityKind] = ids
	}
	return targets, errors.Join(failures...)
}

// recordHealthDiscoveryError marks incomplete coverage without hiding usable members.
func recordHealthDiscoveryError(report *observation.DCGMHealthReport, systems uint64, failure error) {
	if report.SystemErrors == nil {
		report.SystemErrors = make(map[uint64]*observation.CollectionError)
	}
	for system := uint64(1); system != 0; system <<= 1 {
		if systems&system != 0 {
			detail := collectionError(failure)
			detail.Source, detail.Operation = Name, "health discovery"
			report.SystemErrors[system] = &detail
		}
	}
}

// groupMembers reads current membership, including removals made outside this source.
func (watch *nativeHealthWatch) groupMembers() ([]godcgm.GroupEntityPair, error) {
	info, err := watch.ops.info(watch.group)
	if err != nil {
		return nil, fmt.Errorf("read DCGM health group: %w", dcgmErrorFromError(err))
	}
	if info == nil {
		return nil, fmt.Errorf("DCGM returned no health group information")
	}
	return info.EntityList, nil
}

// healthSystemsForEntities maps native group members to requested health systems.
func healthSystemsForEntities(requested uint64, entities []godcgm.GroupEntityPair) uint64 {
	var systems uint64
	for _, entity := range entities {
		for _, selection := range healthEntitySelections {
			if entity.EntityGroupId == selection.entityKind {
				systems |= requested & selection.systems
			}
		}
	}
	return systems
}

// configureGroup repairs membership before enabling watches for new members.
// Rejected members limit coverage but leave usable members and their history intact.
func (watch *nativeHealthWatch) configureGroup(targets map[godcgm.Field_Entity_Group][]uint, members []godcgm.GroupEntityPair, report *observation.DCGMHealthReport, results map[godcgm.GroupEntityPair]*observation.DCGMHealthEntityResult) ([]godcgm.GroupEntityPair, error) {
	// True means an original member; false deduplicates a new discovery without
	// claiming that it has joined the group.
	wasMember := make(map[godcgm.GroupEntityPair]bool, len(members))
	for _, entity := range members {
		wasMember[entity] = true
	}
	var missing []godcgm.GroupEntityPair
	for _, selection := range healthEntitySelections {
		ids := targets[selection.entityKind]
		for _, id := range ids {
			entity := godcgm.GroupEntityPair{EntityGroupId: selection.entityKind, EntityId: id}
			if _, seen := wasMember[entity]; !seen {
				missing = append(missing, entity)
				wasMember[entity] = false
			}
		}
	}
	// No-target reads need no native group. Try candidates independently so a
	// rejected device cannot prevent other devices from joining.
	if !watch.ownsGroup && len(missing) != 0 {
		group, err := watch.ops.create(watch.name)
		if err != nil {
			return nil, fmt.Errorf("create DCGM health group: %w", dcgmErrorFromError(err))
		}
		watch.group, watch.ownsGroup = group, true
	}
	addFailures := make(map[godcgm.GroupEntityPair]error)
	for _, entity := range missing {
		if err := watch.ops.add(watch.group, entity.EntityGroupId, entity.EntityId); err != nil {
			code, _ := dcgmErrorCode(err)
			failure := fmt.Errorf("add entity kind %d ID %d to DCGM health group: %w", entity.EntityGroupId, entity.EntityId, dcgmErrorFromError(err))
			// A missing group or invalid connection affects every member.
			if errors.Is(failure, errConnectionNotValid) || code == godcgm.DCGM_ST_NOT_CONFIGURED {
				return nil, watch.rollbackSetup(failure)
			}
			addFailures[entity] = failure
		}
	}
	added := false
	if len(missing) != 0 {
		// An add can time out after succeeding. Verify actual membership before
		// deciding which devices need watches and which errors still limit coverage.
		var err error
		members, err = watch.groupMembers()
		if err != nil {
			return nil, watch.rollbackSetup(err)
		}
		for _, entity := range members {
			delete(addFailures, entity)
			if !wasMember[entity] {
				added = true
			}
		}
	}
	// Report failed additions as incomplete coverage while usable members
	// continue contributing health findings.
	var failures []error
	for _, entity := range missing {
		if failure := addFailures[entity]; failure != nil {
			detail := collectionError(failure)
			detail.Source, detail.Operation = Name, "health membership"
			result := healthEntityResult(report, results, entity)
			result.CollectionErrors = append(result.CollectionErrors, &observation.DCGMHealthEntityError{
				Systems: healthSystemsForEntities(watch.systems, []godcgm.GroupEntityPair{entity}),
				Error:   &detail,
			})
			failures = append(failures, failure)
		}
	}
	if len(failures) != 0 && len(members) == 0 {
		// Keep rejected targets structured even when none could join. No native
		// result is available; destroy the empty group before the next read retries.
		watch.configured = false
		if err := watch.destroyOwnedGroup(); err != nil {
			return nil, errors.Join(append(failures, err)...)
		}
		return nil, nil
	}
	// Use verified members, including existing ones whose kind's discovery failed.
	systems := healthSystemsForEntities(watch.systems, members)
	if systems == 0 {
		if watch.ownsGroup && !watch.configured {
			return nil, watch.destroyOwnedGroup()
		}
		return members, nil
	}
	if watch.configured && watch.configuredSystems == systems && !added {
		return members, nil
	}
	// The system mask can be unchanged when a GPU returns. Reapply it so DCGM
	// installs field watches for every current member, including that GPU.
	if err := watch.ops.set(watch.group, godcgm.HealthSystem(systems)); err != nil {
		return nil, watch.rollbackSetup(fmt.Errorf("enable DCGM health systems: %w", dcgmErrorFromError(err)))
	}
	// Extending this group keeps its original start time; new members and
	// systems can have less history than the group's first setup.
	if !watch.configured {
		watch.monitoredSince = time.Now()
	}
	watch.configuredSystems, watch.configured = systems, true
	return members, nil
}

func healthEntityResult(report *observation.DCGMHealthReport, results map[godcgm.GroupEntityPair]*observation.DCGMHealthEntityResult, entity godcgm.GroupEntityPair) *observation.DCGMHealthEntityResult {
	if result := results[entity]; result != nil {
		return result
	}
	member := false
	result := &observation.DCGMHealthEntityResult{Entity: healthEntity(entity), GroupMember: &member}
	results[entity] = result
	report.EntityResults = append(report.EntityResults, result)
	return result
}

// rollbackSetup retains a group until native destruction succeeds.
func (watch *nativeHealthWatch) rollbackSetup(cause error) error {
	// Membership or watch setup could not establish a usable configuration.
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

// destroyOwnedGroup clears ownership after destruction or confirmed absence.
func (watch *nativeHealthWatch) destroyOwnedGroup() error {
	if !watch.ownsGroup {
		return nil
	}
	if err := watch.ops.destroy(watch.group); err != nil {
		// DCGM has already removed this group; there is nothing left to destroy.
		if code, ok := dcgmErrorCode(err); !ok || code != godcgm.DCGM_ST_NOT_CONFIGURED {
			return fmt.Errorf("destroy DCGM health group: %w", dcgmErrorFromError(err))
		}
	}
	watch.ownsGroup, watch.configured = false, false
	watch.configuredSystems = 0
	watch.monitoredSince = time.Time{}
	return nil
}
