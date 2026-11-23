//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

type nativeHealthFixture struct {
	entities        map[godcgm.Field_Entity_Group][]uint
	discoveryErrors map[godcgm.Field_Entity_Group]error
	discoveryCalls  map[godcgm.Field_Entity_Group]int
	failures        map[string]error
	calls           []string
	masks           []godcgm.HealthSystem
	members         []godcgm.GroupEntityPair
	response        godcgm.HealthResponse
}

func newNativeHealthFixture() *nativeHealthFixture {
	return &nativeHealthFixture{
		entities: map[godcgm.Field_Entity_Group][]uint{
			godcgm.FE_GPU: {7}, godcgm.FE_SWITCH: {12},
		},
		discoveryErrors: make(map[godcgm.Field_Entity_Group]error),
		discoveryCalls:  make(map[godcgm.Field_Entity_Group]int),
		failures:        make(map[string]error),
	}
}

func (fixture *nativeHealthFixture) call(operation string) error {
	fixture.calls = append(fixture.calls, operation)
	return fixture.failures[operation]
}

func (fixture *nativeHealthFixture) count(operation string) int {
	var count int
	for _, call := range fixture.calls {
		if call == operation {
			count++
		}
	}
	return count
}

func (fixture *nativeHealthFixture) operations() healthWatchOperations {
	return healthWatchOperations{
		entities: func(kind godcgm.Field_Entity_Group) ([]uint, error) {
			fixture.discoveryCalls[kind]++
			if err := fixture.call("entities"); err != nil {
				return nil, err
			}
			return slices.Clone(fixture.entities[kind]), fixture.discoveryErrors[kind]
		},
		create: func(string) (godcgm.GroupHandle, error) {
			var group godcgm.GroupHandle
			if err := fixture.call("create"); err != nil {
				return group, err
			}
			group.SetHandle(uintptr(fixture.count("create")))
			fixture.members = nil
			return group, nil
		},
		add: func(_ godcgm.GroupHandle, kind godcgm.Field_Entity_Group, id uint) error {
			if err := fixture.call("add"); err != nil {
				return err
			}
			fixture.members = append(fixture.members, godcgm.GroupEntityPair{EntityGroupId: kind, EntityId: id})
			return nil
		},
		set: func(_ godcgm.GroupHandle, systems godcgm.HealthSystem) error {
			fixture.masks = append(fixture.masks, systems)
			return fixture.call("set")
		},
		info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
			if err := fixture.call("info"); err != nil {
				return nil, err
			}
			return &godcgm.GroupInfo{EntityList: slices.Clone(fixture.members)}, nil
		},
		check: func(godcgm.GroupHandle) (godcgm.HealthResponse, error) {
			return fixture.response, fixture.call("check")
		},
		destroy: func(godcgm.GroupHandle) error {
			if err := fixture.call("destroy"); err != nil {
				return err
			}
			fixture.members = nil
			return nil
		},
	}
}

func TestHealthSystemMasksMatchNative(t *testing.T) {
	for _, pair := range []struct {
		portable uint64
		native   godcgm.HealthSystem
	}{
		{dcgmhealth.SystemPCIe, godcgm.DCGM_HEALTH_WATCH_PCIE},
		{dcgmhealth.SystemMemory, godcgm.DCGM_HEALTH_WATCH_MEM},
		{dcgmhealth.SystemInfoROM, godcgm.DCGM_HEALTH_WATCH_INFOROM},
		{dcgmhealth.SystemThermal, godcgm.DCGM_HEALTH_WATCH_THERMAL},
		{dcgmhealth.SystemPower, godcgm.DCGM_HEALTH_WATCH_POWER},
		{dcgmhealth.SystemDriver, godcgm.DCGM_HEALTH_WATCH_DRIVER},
		{dcgmhealth.SystemNVLink, godcgm.DCGM_HEALTH_WATCH_NVLINK},
		{dcgmhealth.SystemNVSwitchFatal, godcgm.DCGM_HEALTH_WATCH_NVSWITCH_FATAL},
		{dcgmhealth.SystemNVSwitchNonfatal, godcgm.DCGM_HEALTH_WATCH_NVSWITCH_NONFATAL},
		{dcgmhealth.SystemAll, godcgm.DCGM_HEALTH_WATCH_ALL},
	} {
		if pair.portable != uint64(pair.native) {
			t.Errorf("portable mask %#x != native %#x", pair.portable, pair.native)
		}
	}
}

func TestHealthWatchSamplingParametersReachNativeSetup(t *testing.T) {
	for _, constructor := range []struct {
		newSource func(*HealthWatchOptions) *Source
		name      string
	}{
		{name: "standalone", newSource: func(options *HealthWatchOptions) *Source {
			return NewStandaloneSource(StandaloneOptions{HealthWatch: options})
		}},
		{name: "embedded", newSource: func(options *HealthWatchOptions) *Source {
			return NewEmbeddedSource(EmbeddedOptions{HealthWatch: options})
		}},
	} {
		for _, custom := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/custom=%t", constructor.name, custom), func(t *testing.T) {
				var options *HealthWatchOptions
				want := HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: 10 * time.Minute}
				if custom {
					want = HealthWatchOptions{UpdateInterval: 75*time.Second + time.Microsecond, MaxKeepAge: 1500*time.Second + time.Millisecond}
					copied := want
					options = &copied
				}
				source := constructor.newSource(options)
				library := source.native.(*nativeLibrary)
				if custom {
					options.UpdateInterval, options.MaxKeepAge = time.Second, time.Second
				}
				var calls int
				library.healthSet = func(group godcgm.GroupHandle, systems godcgm.HealthSystem, interval, retention time.Duration) error {
					calls++
					if group.GetHandle() == 0 || uint64(systems) != dcgmhealth.SystemMemory || interval != want.UpdateInterval || retention != want.MaxKeepAge {
						t.Fatalf("native setup = %v, %#x, %v, %v; want %+v", group, systems, interval, retention, want)
					}
					if calls == 1 {
						return errors.New("temporary setup failure")
					}
					return nil
				}
				// Every new session constructs a watch with the source's copied settings.
				for session := 0; session < 2; session++ {
					created, err := library.WatchHealth(dcgmhealth.SystemMemory)
					if err != nil {
						t.Fatal(err)
					}
					watch := created.(*nativeHealthWatch)
					fixture := newNativeHealthFixture()
					ops := fixture.operations()
					ops.set = watch.ops.set
					watch.ops = ops
					if session == 0 {
						if _, err := watch.Read(); err == nil {
							t.Fatal("first setup unexpectedly succeeded")
						}
					}
					if _, err := watch.Read(); err != nil {
						t.Fatal(err)
					}
					if err := watch.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 3 {
					t.Fatalf("native setup calls = %d; want 3", calls)
				}
			})
		}
	}
}

func TestNativeHealthStatusConversion(t *testing.T) {
	for _, test := range []struct {
		name     string
		native   godcgm.HealthResult
		portable observation.DCGMHealthResult
	}{
		{name: "pass", native: godcgm.DCGM_HEALTH_RESULT_PASS, portable: observation.DCGMHealthResultPass},
		{name: "warn", native: godcgm.DCGM_HEALTH_RESULT_WARN, portable: observation.DCGMHealthResultWarn},
		{name: "fail", native: godcgm.DCGM_HEALTH_RESULT_FAIL, portable: observation.DCGMHealthResultFail},
		{name: "unknown positive", native: 987, portable: 987},
		{name: "unknown negative", native: godcgm.HealthResult(^uint(0)), portable: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			watch := &nativeHealthWatch{
				owned: true, configured: true, systems: dcgmhealth.SystemMemory, configuredSystems: dcgmhealth.SystemMemory,
				targets: map[godcgm.Field_Entity_Group][]uint{godcgm.FE_GPU: {0}},
				ops: healthWatchOperations{
					info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
						return &godcgm.GroupInfo{EntityList: []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU}}}, nil
					},
					check: func(godcgm.GroupHandle) (godcgm.HealthResponse, error) {
						return godcgm.HealthResponse{OverallHealth: test.native, Incidents: []godcgm.Incident{{Health: test.native}}}, nil
					},
				},
			}
			report, err := watch.Read()
			if err != nil || report.NativeGroupOverallHealth == nil || report.GetNativeGroupOverallHealth() != test.portable ||
				len(report.Incidents) != 1 || report.Incidents[0].NativeHealth == nil || report.Incidents[0].GetNativeHealth() != test.portable {
				t.Fatalf("native status %v converted to %v, %v", test.native, report, err)
			}
		})
	}
}

func TestNativeHealthSelectsEntitiesAndEnablesConfiguredUnion(t *testing.T) {
	for _, test := range []struct {
		name  string
		kinds []godcgm.Field_Entity_Group
		mask  uint64
	}{
		{name: "GPU", mask: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe, kinds: []godcgm.Field_Entity_Group{godcgm.FE_GPU}},
		{name: "driver", mask: dcgmhealth.SystemDriver, kinds: []godcgm.Field_Entity_Group{godcgm.FE_GPU}},
		{name: "NVSwitch", mask: dcgmhealth.SystemNVSwitchFatal, kinds: []godcgm.Field_Entity_Group{godcgm.FE_SWITCH}},
		{name: "combined", mask: dcgmhealth.KnownSystems, kinds: []godcgm.Field_Entity_Group{godcgm.FE_GPU, godcgm.FE_SWITCH}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var kinds []godcgm.Field_Entity_Group
			var added []godcgm.GroupEntityPair
			var configured godcgm.HealthSystem
			var setCalls int
			var group godcgm.GroupHandle
			group.SetHandle(42)
			watch, err := startHealthWatch("owned-health", test.mask, healthWatchOperations{
				entities: func(kind godcgm.Field_Entity_Group) ([]uint, error) {
					kinds = append(kinds, kind)
					return []uint{7}, nil
				},
				create: func(name string) (godcgm.GroupHandle, error) {
					if name != "owned-health" {
						t.Fatalf("group name = %q", name)
					}
					return group, nil
				},
				add: func(handle godcgm.GroupHandle, kind godcgm.Field_Entity_Group, id uint) error {
					if handle != group {
						t.Fatalf("add to unexpected group = %v", handle)
					}
					added = append(added, godcgm.GroupEntityPair{EntityGroupId: kind, EntityId: id})
					return nil
				},
				set: func(handle godcgm.GroupHandle, systems godcgm.HealthSystem) error {
					if handle != group {
						t.Fatalf("configure unexpected group = %v", handle)
					}
					configured = systems
					setCalls++
					return nil
				},
				info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
					return &godcgm.GroupInfo{EntityList: slices.Clone(added)}, nil
				},
				check: func(godcgm.GroupHandle) (godcgm.HealthResponse, error) {
					return godcgm.HealthResponse{}, nil
				},
				destroy: func(godcgm.GroupHandle) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if len(kinds) != 0 || len(added) != 0 || setCalls != 0 {
				t.Fatal("constructing a health watch performed native setup")
			}
			for range 2 {
				if _, err := watch.Read(); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(kinds, test.kinds) || uint64(configured) != test.mask || setCalls != 1 || len(added) != len(test.kinds) {
				t.Fatalf("enumerated=%v added=%v configured=%#x", kinds, added, configured)
			}
			for i, kind := range test.kinds {
				if added[i].EntityGroupId != kind || added[i].EntityId != 7 {
					t.Fatalf("unexpected group entity = %v", added[i])
				}
			}
		})
	}
}

func TestNativeHealthRollbackAttemptsOwnedGroupOnce(t *testing.T) {
	setupErr, cleanupErr := errors.New("health setup failed"), errors.New("group cleanup failed")
	for _, failAt := range []string{"add", "set"} {
		t.Run(failAt, func(t *testing.T) {
			var calls []string
			var group godcgm.GroupHandle
			// Group zero is a valid owned handle; do not confuse it with no group.
			ops := healthWatchOperations{
				entities: func(kind godcgm.Field_Entity_Group) ([]uint, error) { return []uint{0}, nil },
				create: func(string) (godcgm.GroupHandle, error) {
					calls = append(calls, "create")
					return group, nil
				},
				add: func(godcgm.GroupHandle, godcgm.Field_Entity_Group, uint) error {
					calls = append(calls, "add")
					if failAt == "add" {
						return setupErr
					}
					return nil
				},
				set: func(godcgm.GroupHandle, godcgm.HealthSystem) error {
					calls = append(calls, "set")
					return setupErr
				},
				destroy: func(godcgm.GroupHandle) error {
					calls = append(calls, "destroy")
					return cleanupErr
				},
			}
			watch, err := startHealthWatch("test-health", dcgmhealth.SystemPCIe, ops)
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if len(calls) != 0 {
				t.Fatalf("watch construction performed native calls: %v", calls)
			}
			report, err := watch.Read()
			if report != nil || !errors.Is(err, setupErr) || !errors.Is(err, cleanupErr) {
				t.Fatalf("setup returned report=%v err=%v", report, err)
			}
			want := []string{"create", "add", "destroy"}
			if failAt == "set" {
				want = []string{"create", "add", "set", "destroy"}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
		})
	}
}

func TestNativeHealthNoTargetsDoesNotReportPassOrCreateGroup(t *testing.T) {
	var kinds []godcgm.Field_Entity_Group
	watch, err := startHealthWatch("no-targets", dcgmhealth.KnownSystems, healthWatchOperations{
		entities: func(kind godcgm.Field_Entity_Group) ([]uint, error) {
			kinds = append(kinds, kind)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := watch.Read()
	if err != nil || report.NativeGroupOverallHealth != nil || report.GroupId != nil || report.MonitoredSince != nil ||
		len(report.Entities) != 0 || report.GetSystemsWithoutTargets() != dcgmhealth.KnownSystems || len(report.GetSystemErrors()) != 0 {
		t.Fatalf("no-target report=%v err=%v", report, err)
	}
	if !reflect.DeepEqual(kinds, []godcgm.Field_Entity_Group{godcgm.FE_GPU, godcgm.FE_SWITCH}) {
		t.Fatalf("enumerated entity kinds=%v", kinds)
	}
	if err := watch.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeHealthSetupFailuresRetryOnNextRead(t *testing.T) {
	for _, operation := range []string{"entities", "create", "add", "set"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			fixture.failures[operation] = failure
			watch, err := startHealthWatch("retry-setup", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if report, err := watch.Read(); report != nil || !errors.Is(err, failure) {
				t.Fatalf("setup failure returned report=%v err=%v", report, err)
			} else if detail := collectionError(err); detail.SourceError != "DCGM_ST_NO_DATA" {
				t.Fatalf("setup failure lost native status: %v", &detail)
			}
			if fixture.count(operation) != 1 {
				t.Fatalf("failed setup retried within one read: %v", fixture.calls)
			}
			wantRollback := 0
			if operation == "add" || operation == "set" {
				wantRollback = 1
			}
			if fixture.count("destroy") != wantRollback {
				t.Fatalf("setup cleanup calls=%v", fixture.calls)
			}
			delete(fixture.failures, operation)
			for range 2 {
				report, err := watch.Read()
				if err != nil || report.GetNativeGroupOverallHealth() != observation.DCGMHealthResultPass ||
					report.NativeGroupOverallHealth == nil || report.MonitoredSince == nil || len(report.Entities) != 1 {
					t.Fatalf("setup did not recover: report=%v err=%v", report, err)
				}
			}
			if fixture.count(operation) != 2 || fixture.count("check") != 2 || fixture.count("destroy") != wantRollback {
				t.Fatalf("successful setup was repeated: %v", fixture.calls)
			}
		})
	}
}

func TestNativeHealthFailedRollbackRetainsGroupUntilDestructionSucceeds(t *testing.T) {
	for _, operation := range []string{"add", "set"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			setupErr := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			cleanupErr := &godcgm.Error{Code: godcgm.DCGM_ST_NO_PERMISSION}
			fixture.failures[operation], fixture.failures["destroy"] = setupErr, cleanupErr
			watch, err := startHealthWatch("retry-rollback", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, setupErr) || !errors.Is(readErr, cleanupErr) {
				t.Fatalf("setup lost joined failures: report=%v err=%v", report, readErr)
			}
			native := watch.(*nativeHealthWatch)
			if !native.owned || native.configured || !native.monitoredSince.IsZero() || fixture.count("destroy") != 1 {
				t.Fatalf("failed rollback lost group ownership: watch=%+v calls=%v", native, fixture.calls)
			}
			previousCalls := len(fixture.calls)
			delete(fixture.failures, operation)
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, cleanupErr) {
				t.Fatalf("pending rollback failure: report=%v err=%v", report, readErr)
			}
			if !slices.Equal(fixture.calls[previousCalls:], []string{"destroy"}) {
				t.Fatalf("setup restarted before cleanup succeeded: %v", fixture.calls)
			}
			delete(fixture.failures, "destroy")
			report, err := watch.Read()
			if err != nil || report.GetGroupId() != 2 || !native.owned || !native.configured || fixture.count("destroy") != 3 || fixture.count("create") != 2 {
				t.Fatalf("pending rollback did not recover: report=%v err=%v calls=%v", report, err, fixture.calls)
			}
		})
	}
}

func TestNativeHealthPartialDiscoveryRecoversWithoutReconfiguringKnownTargets(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.discoveryErrors[godcgm.FE_SWITCH] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_SUPPORTED}
	const gpuSystems = dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe
	const requested = gpuSystems | dcgmhealth.NVSwitchSystems
	watch, err := startHealthWatch("partial-discovery", requested, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Read()
	if err != nil || len(first.GetEntities()) != 1 || len(first.GetSystemErrors()) != 2 ||
		first.GetRequestedSystems() != requested || first.GetSystemsWithoutTargets() != 0 || first.NativeGroupOverallHealth == nil {
		t.Fatalf("partial discovery report=%v err=%v", first, err)
	}
	for _, system := range []uint64{dcgmhealth.SystemNVSwitchFatal, dcgmhealth.SystemNVSwitchNonfatal} {
		failure := first.GetSystemErrors()[system]
		if failure.GetSource() != Name || failure.GetOperation() != "health discovery" ||
			failure.GetSourceError() != "DCGM_ST_NOT_SUPPORTED" || failure.GetCategory() != observation.CollectionErrorCategoryUnsupported {
			t.Fatalf("discovery error for system %#x = %v", system, failure)
		}
	}
	if _, err := watch.Read(); err != nil {
		t.Fatal(err)
	}
	if fixture.discoveryCalls[godcgm.FE_GPU] != 1 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 2 || fixture.count("set") != 1 {
		t.Fatalf("known targets were rediscovered or reconfigured: %+v calls=%v", fixture.discoveryCalls, fixture.calls)
	}
	delete(fixture.discoveryErrors, godcgm.FE_SWITCH)
	for range 2 {
		report, err := watch.Read()
		if err != nil || len(report.GetEntities()) != 2 || len(report.GetSystemErrors()) != 0 || report.GetSystemsWithoutTargets() != 0 ||
			report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) {
			t.Fatalf("discovery recovery report=%v err=%v", report, err)
		}
	}
	if fixture.discoveryCalls[godcgm.FE_GPU] != 1 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 3 || fixture.count("create") != 1 ||
		fixture.count("add") != 2 || !slices.Equal(fixture.masks, []godcgm.HealthSystem{godcgm.HealthSystem(gpuSystems), godcgm.HealthSystem(requested)}) || len(first.GetSystemErrors()) != 2 {
		t.Fatalf("discovery recovery repeated setup or changed earlier report: calls=%v masks=%v", fixture.calls, fixture.masks)
	}
}

func TestNativeHealthRetriesKnownEmptyEntityKinds(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.entities[godcgm.FE_SWITCH] = nil
	const requested = dcgmhealth.SystemMemory | dcgmhealth.NVSwitchSystems
	watch, err := startHealthWatch("empty-kind", requested, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	var groupID uint64
	for range 2 {
		report, readErr := watch.Read()
		if readErr != nil || report.GetSystemsWithoutTargets() != dcgmhealth.NVSwitchSystems || len(report.GetSystemErrors()) != 0 || len(report.GetEntities()) != 1 {
			t.Fatalf("known empty kind report=%v err=%v", report, readErr)
		}
		groupID = report.GetGroupId()
	}
	if fixture.count("set") != 1 || fixture.discoveryCalls[godcgm.FE_GPU] != 1 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 2 {
		t.Fatalf("empty discovery reconfigured known targets: calls=%v", fixture.calls)
	}
	fixture.entities[godcgm.FE_SWITCH] = []uint{12}
	report, err := watch.Read()
	if err != nil || report.GetSystemsWithoutTargets() != 0 || len(report.GetEntities()) != 2 || report.GetGroupId() != groupID || fixture.count("set") != 2 {
		t.Fatalf("new targets were not configured: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
}

func TestNativeHealthDiscoveryErrorsAreNotMissingTargets(t *testing.T) {
	for _, allErrors := range []bool{false, true} {
		t.Run(map[bool]string{false: "known empty GPU and failed switches", true: "all kinds fail"}[allErrors], func(t *testing.T) {
			fixture := newNativeHealthFixture()
			gpuErr, switchErr := errors.New("GPU discovery failed"), errors.New("switch discovery failed")
			fixture.entities[godcgm.FE_GPU] = nil
			fixture.discoveryErrors[godcgm.FE_SWITCH] = switchErr
			if allErrors {
				fixture.discoveryErrors[godcgm.FE_GPU] = gpuErr
			}
			const requested = dcgmhealth.SystemMemory | dcgmhealth.NVSwitchSystems
			watch, err := startHealthWatch("discovery-errors", requested, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			report, err := watch.Read()
			if allErrors {
				if report != nil || !errors.Is(err, gpuErr) || !errors.Is(err, switchErr) {
					t.Fatalf("all discovery errors: report=%v err=%v", report, err)
				}
			} else if err != nil || report.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory || len(report.GetSystemErrors()) != 2 ||
				report.GroupId != nil || report.NativeGroupOverallHealth != nil || report.MonitoredSince != nil {
				t.Fatalf("mixed empty and failed discovery: report=%v err=%v", report, err)
			}
			if fixture.count("create") != 0 || fixture.count("check") != 0 {
				t.Fatalf("failed discovery created a native group: %v", fixture.calls)
			}
		})
	}
}

func TestNativeHealthConnectionFailureIsNotPartialDiscovery(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.discoveryErrors[godcgm.FE_SWITCH] = &godcgm.Error{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}
	watch, err := startHealthWatch("connection-failure", dcgmhealth.KnownSystems, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if report, err := watch.Read(); report != nil || !errors.Is(err, errConnectionNotValid) {
		t.Fatalf("connection failure returned partial findings: report=%v err=%v", report, err)
	}
	if fixture.count("create") != 0 || fixture.count("check") != 0 {
		t.Fatalf("connection failure continued native setup: %v", fixture.calls)
	}
}

func TestNativeHealthReconfigurationFailureRebuildsGroup(t *testing.T) {
	for _, operation := range []string{"add", "set"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newNativeHealthFixture()
				fixture.entities[godcgm.FE_SWITCH] = nil
				watch, err := startHealthWatch("reconfigure", dcgmhealth.KnownSystems, fixture.operations())
				if err != nil {
					t.Fatal(err)
				}
				defer watch.Close()
				first, err := watch.Read()
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				fixture.entities[godcgm.FE_SWITCH] = []uint{12}
				failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
				fixture.failures[operation] = failure
				if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
					t.Fatalf("reconfiguration failure: report=%v err=%v", report, readErr)
				}
				if fixture.count("destroy") != 1 || fixture.count("create") != 1 || watch.(*nativeHealthWatch).owned {
					t.Fatalf("failed reconfiguration retained a usable group: %v", fixture.calls)
				}
				delete(fixture.failures, operation)
				time.Sleep(time.Second)
				report, err := watch.Read()
				if err != nil || report.GetGroupId() == first.GetGroupId() || len(report.GetEntities()) != 2 || len(report.GetSystemErrors()) != 0 ||
					report.GetSystemsWithoutTargets() != 0 || !report.GetMonitoredSince().AsTime().After(first.GetMonitoredSince().AsTime()) {
					t.Fatalf("group was not rebuilt: report=%v err=%v", report, err)
				}
				if fixture.count("create") != 2 || fixture.discoveryCalls[godcgm.FE_GPU] != 2 {
					t.Fatalf("rebuild reused stale setup: calls=%v discoveries=%v", fixture.calls, fixture.discoveryCalls)
				}
			})
		})
	}
}

func TestNativeHealthQueryFailureKeepsConfiguredGroup(t *testing.T) {
	for _, operation := range []string{"info", "check"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			fixture.failures[operation] = failure
			watch, err := startHealthWatch("query-failure", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
				t.Fatalf("query failure: report=%v err=%v", report, readErr)
			}
			native := watch.(*nativeHealthWatch)
			monitoredSince := native.monitoredSince
			delete(fixture.failures, operation)
			report, err := watch.Read()
			if err != nil || report.GetGroupId() != uint64(native.group.GetHandle()) || !report.GetMonitoredSince().AsTime().Equal(monitoredSince) ||
				fixture.count("create") != 1 || fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.discoveryCalls[godcgm.FE_GPU] != 1 {
				t.Fatalf("query failure rebuilt the group: report=%v err=%v calls=%v", report, err, fixture.calls)
			}
		})
	}
}

func TestNativeHealthReportMarksIncidentCapacity(t *testing.T) {
	for _, count := range []int{maxNativeHealthIncidents - 1, maxNativeHealthIncidents} {
		fixture := newNativeHealthFixture()
		fixture.response.Incidents = make([]godcgm.Incident, count)
		watch, err := startHealthWatch("incident-capacity", dcgmhealth.SystemMemory, fixture.operations())
		if err != nil {
			t.Fatal(err)
		}
		report, err := watch.Read()
		closeErr := watch.Close()
		if err != nil || closeErr != nil || len(report.GetIncidents()) != count || report.GetPossiblyTruncated() != (count == maxNativeHealthIncidents) {
			t.Fatalf("incident count %d: report=%v err=%v close=%v", count, report, err, closeErr)
		}
	}
}

func TestNativeHealthPreservesUnknownIncidentsAndEntityIDs(t *testing.T) {
	var calls []string
	var group godcgm.GroupHandle
	group.SetHandle(42)
	watch := &nativeHealthWatch{
		group: group, systems: dcgmhealth.SystemMemory, owned: true, configured: true, configuredSystems: dcgmhealth.SystemMemory,
		targets: map[godcgm.Field_Entity_Group][]uint{godcgm.FE_GPU: {0}},
		ops: healthWatchOperations{
			info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
				return &godcgm.GroupInfo{EntityList: []godcgm.GroupEntityPair{
					{EntityGroupId: godcgm.FE_GPU, EntityId: 0},
					{EntityGroupId: godcgm.FE_SWITCH, EntityId: 3},
				}}, nil
			},
			check: func(godcgm.GroupHandle) (godcgm.HealthResponse, error) {
				calls = append(calls, "check")
				return godcgm.HealthResponse{OverallHealth: godcgm.DCGM_HEALTH_RESULT_FAIL, Incidents: []godcgm.Incident{
					{
						System: godcgm.HealthSystem(1 << 30), Health: godcgm.HealthResult(71),
						Error:      godcgm.DiagErrorDetail{Code: godcgm.HealthCheckErrorCode(123456), Message: "future incident"},
						EntityInfo: godcgm.GroupEntityPair{EntityGroupId: godcgm.Field_Entity_Group(123), EntityId: 99},
					},
				}}, nil
			},
			destroy: func(godcgm.GroupHandle) error {
				calls = append(calls, "destroy")
				return errors.New("destroy failed")
			},
		},
	}
	report, err := watch.Read()
	if err != nil {
		t.Fatal(err)
	}
	if report.GetGroupId() != 42 || report.GetNativeGroupOverallHealth() != 20 || len(report.Entities) != 2 {
		t.Fatalf("group metadata=%v", report)
	}
	incident := report.Incidents[0]
	if incident.System != 1<<30 || incident.GetNativeHealth() != 71 || incident.ErrorCode != 123456 ||
		incident.Message != "future incident" || incident.Entity.GetNativeEntity().GetSource() != Name ||
		incident.Entity.GetNativeEntity().GetEntityKind() != 123 || incident.Entity.GetNativeEntity().GetEntityId() != 99 {
		t.Fatalf("incident lost native details: %v", incident)
	}
	if err := watch.Close(); err == nil {
		t.Fatal("close discarded cleanup error")
	}
	if err := watch.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Read(); !errors.Is(err, errClosed) {
		t.Fatalf("read after close=%v", err)
	}
	if !reflect.DeepEqual(calls, []string{"check", "destroy"}) {
		t.Fatalf("native calls=%v", calls)
	}
}

func TestNativeHealthEntitiesUseSharedTargets(t *testing.T) {
	for _, test := range []struct {
		name   string
		entity godcgm.GroupEntityPair
	}{
		{name: "GPU zero", entity: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU}},
		{name: "GPU", entity: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}},
		{name: "NVSwitch zero", entity: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH}},
		{name: "NVSwitch", entity: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 3}},
		{name: "unknown", entity: godcgm.GroupEntityPair{EntityGroupId: godcgm.Field_Entity_Group(71), EntityId: 99}},
		{name: "zero", entity: godcgm.GroupEntityPair{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := healthEntity(test.entity)
			var native *observation.NativeEntityRef
			switch test.entity.EntityGroupId {
			case godcgm.FE_GPU:
				gpu := target.GetGpu()
				if gpu == nil || gpu.GetUuid() != "" || gpu.GetSourceLocalId() != "" {
					t.Fatalf("GPU identity = %v", target)
				}
				native = gpu.GetNative()
			case godcgm.FE_SWITCH:
				switchRef := target.GetNvSwitch()
				if switchRef == nil || switchRef.GetUuid() != "" {
					t.Fatalf("NVSwitch identity = %v", target)
				}
				native = switchRef.GetNative()
			default:
				native = target.GetNativeEntity()
			}
			if native == nil || native.GetSource() != Name || native.GetEntityKind() != uint32(test.entity.EntityGroupId) || native.GetEntityId() != uint32(test.entity.EntityId) {
				t.Fatalf("native identity = %v, want %v", target, test.entity)
			}
		})
	}
}
