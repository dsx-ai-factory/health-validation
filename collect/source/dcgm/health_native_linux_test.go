//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
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
	addErrors       map[godcgm.GroupEntityPair]error
	committedAdds   map[godcgm.GroupEntityPair]bool
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
		addErrors:       make(map[godcgm.GroupEntityPair]error),
		committedAdds:   make(map[godcgm.GroupEntityPair]bool),
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
			member := godcgm.GroupEntityPair{EntityGroupId: kind, EntityId: id}
			if err := fixture.addErrors[member]; err != nil && !fixture.committedAdds[member] {
				return err
			}
			if slices.Contains(fixture.members, member) {
				return fmt.Errorf("entity %v already belongs to the group", member)
			}
			fixture.members = append(fixture.members, member)
			return fixture.addErrors[member]
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

func TestNativeHealthUsesActiveGPUDiscovery(t *testing.T) {
	for _, discoveryError := range []error{nil, &godcgm.Error{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}} {
		t.Run(fmt.Sprintf("error=%v", discoveryError), func(t *testing.T) {
			library := newNative(StandaloneOptions{}, standaloneMode).(*nativeLibrary)
			var calls int
			library.healthGPUIndices = func() ([]uint, error) {
				calls++
				if discoveryError != nil {
					return nil, discoveryError
				}
				return []uint{7}, nil
			}
			created, err := library.WatchHealth(dcgmhealth.SystemMemory)
			if err != nil {
				t.Fatal(err)
			}
			watch := created.(*nativeHealthWatch)
			ids, err := watch.ops.entities(godcgm.FE_GPU)
			if !errors.Is(err, discoveryError) || calls != 1 || (discoveryError == nil && !slices.Equal(ids, []uint{7})) {
				t.Fatalf("active GPU discovery = %v, %v; calls = %d", ids, err, calls)
			}
		})
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
				ops: healthWatchOperations{
					entities: func(godcgm.Field_Entity_Group) ([]uint, error) { return []uint{0}, nil },
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
			wantKinds := append(slices.Clone(test.kinds), test.kinds...)
			if !slices.Equal(kinds, wantKinds) || uint64(configured) != test.mask || setCalls != 1 || len(added) != len(test.kinds) {
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
			var members []godcgm.GroupEntityPair
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
					members = append(members, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU})
					return nil
				},
				info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
					return &godcgm.GroupInfo{EntityList: slices.Clone(members)}, nil
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
			wantOperationCalls := 2
			if operation == "entities" {
				wantOperationCalls = 3
			}
			if fixture.count(operation) != wantOperationCalls || fixture.count("check") != 2 || fixture.count("destroy") != wantRollback {
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
	if fixture.discoveryCalls[godcgm.FE_GPU] != 2 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 2 || fixture.count("set") != 1 {
		t.Fatalf("unchanged targets were not rediscovered once per read or were reconfigured: %+v calls=%v", fixture.discoveryCalls, fixture.calls)
	}
	delete(fixture.discoveryErrors, godcgm.FE_SWITCH)
	for range 2 {
		report, err := watch.Read()
		if err != nil || len(report.GetEntities()) != 2 || len(report.GetSystemErrors()) != 0 || report.GetSystemsWithoutTargets() != 0 ||
			report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) {
			t.Fatalf("discovery recovery report=%v err=%v", report, err)
		}
	}
	if fixture.discoveryCalls[godcgm.FE_GPU] != 4 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 4 || fixture.count("create") != 1 ||
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
	if fixture.count("set") != 1 || fixture.discoveryCalls[godcgm.FE_GPU] != 2 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 2 {
		t.Fatalf("empty discovery reconfigured known targets: calls=%v", fixture.calls)
	}
	fixture.entities[godcgm.FE_SWITCH] = []uint{12}
	report, err := watch.Read()
	if err != nil || report.GetSystemsWithoutTargets() != 0 || len(report.GetEntities()) != 2 || report.GetGroupId() != groupID || fixture.count("set") != 2 {
		t.Fatalf("new targets were not configured: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
}

func requireNativeHealthMembers(t *testing.T, report *observation.DCGMHealthReport, want ...godcgm.GroupEntityPair) {
	t.Helper()
	if len(report.GetEntities()) != len(want) {
		t.Fatalf("health members = %v, want %v", report.GetEntities(), want)
	}
	for position, target := range report.GetEntities() {
		native := target.GetGpu().GetNative()
		if target.GetNvSwitch() != nil {
			native = target.GetNvSwitch().GetNative()
		}
		member := godcgm.GroupEntityPair{EntityGroupId: godcgm.Field_Entity_Group(native.GetEntityKind()), EntityId: uint(native.GetEntityId())}
		if native.GetSource() != Name || member != want[position] {
			t.Fatalf("health member %d = %v, want %v", position, target, want[position])
		}
	}
}

func TestNativeHealthRejectedMembersPreserveCoverageAndRetry(t *testing.T) {
	for _, test := range []struct {
		name      string
		entities  map[godcgm.Field_Entity_Group][]uint
		members   []godcgm.GroupEntityPair
		rejected  godcgm.GroupEntityPair
		requested uint64
		enabled   uint64
		failed    uint64
	}{
		{
			name:      "GPU survives rejected switch",
			entities:  map[godcgm.Field_Entity_Group][]uint{godcgm.FE_GPU: {2}, godcgm.FE_SWITCH: {12}},
			members:   []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU, EntityId: 2}},
			rejected:  godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12},
			requested: dcgmhealth.SystemMemory | dcgmhealth.NVSwitchSystems,
			enabled:   dcgmhealth.SystemMemory, failed: dcgmhealth.NVSwitchSystems,
		},
		{
			name:      "GPU survives rejected GPU",
			entities:  map[godcgm.Field_Entity_Group][]uint{godcgm.FE_GPU: {2, 7}},
			members:   []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU, EntityId: 2}},
			rejected:  godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7},
			requested: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe,
			enabled:   dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe, failed: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe,
		},
		{
			name:      "switch survives rejected switch",
			entities:  map[godcgm.Field_Entity_Group][]uint{godcgm.FE_SWITCH: {11, 12}},
			members:   []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_SWITCH, EntityId: 11}},
			rejected:  godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12},
			requested: dcgmhealth.NVSwitchSystems, enabled: dcgmhealth.NVSwitchSystems, failed: dcgmhealth.NVSwitchSystems,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			fixture.entities = test.entities
			fixture.addErrors[test.rejected] = &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}
			var incidentSystem godcgm.HealthSystem = godcgm.DCGM_HEALTH_WATCH_MEM
			if test.members[0].EntityGroupId == godcgm.FE_SWITCH {
				incidentSystem = godcgm.DCGM_HEALTH_WATCH_NVSWITCH_FATAL
			}
			fixture.response = godcgm.HealthResponse{OverallHealth: godcgm.DCGM_HEALTH_RESULT_FAIL, Incidents: []godcgm.Incident{{
				System: incidentSystem, Health: godcgm.DCGM_HEALTH_RESULT_FAIL, EntityInfo: test.members[0],
				Error: godcgm.DiagErrorDetail{Code: godcgm.HealthCheckErrorCode(37), Message: "usable member fault"},
			}}}
			watch, err := startHealthWatch("partial-membership", test.requested, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			var first *observation.DCGMHealthReport
			for read := range 3 {
				report, readErr := watch.Read()
				if readErr != nil || report.GetNativeGroupOverallHealth() != observation.DCGMHealthResultFail || report.NativeGroupOverallHealth == nil ||
					report.GetSystemsWithoutTargets() != 0 || len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 37 {
					t.Fatalf("rejected member discarded usable coverage: report=%v err=%v", report, readErr)
				}
				requireNativeHealthMembers(t, report, test.members...)
				var failed uint64
				for system, failure := range report.GetSystemErrors() {
					failed |= system
					if system == 0 || system&(system-1) != 0 || failure.GetSource() != Name || failure.GetSourceError() != "DCGM_ST_BADPARAM" ||
						!strings.Contains(failure.GetDetail(), fmt.Sprint(test.rejected.EntityId)) {
						t.Fatalf("membership error lost rejected member or native status: system=%#x failure=%v", system, failure)
					}
				}
				if failed != test.failed {
					t.Fatalf("unavailable systems = %#x, want %#x", failed, test.failed)
				}
				if read == 0 {
					first = report
				} else if report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) {
					t.Fatalf("rejected member restarted usable checks: first=%v current=%v", first, report)
				}
			}
			if fixture.count("create") != 1 || fixture.count("destroy") != 0 || fixture.count("set") != 1 || fixture.count("check") != 3 ||
				fixture.count("add") != len(test.members)+3 || !slices.Equal(fixture.masks, []godcgm.HealthSystem{godcgm.HealthSystem(test.enabled)}) {
				t.Fatalf("rejected additions repeated setup or skipped retry: calls=%v masks=%v", fixture.calls, fixture.masks)
			}
			delete(fixture.addErrors, test.rejected)
			for range 2 {
				report, readErr := watch.Read()
				if readErr != nil || len(report.GetSystemErrors()) != 0 || report.GetSystemsWithoutTargets() != 0 ||
					report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) || len(report.GetIncidents()) != 1 {
					t.Fatalf("rejected member did not recover in its existing group: report=%v err=%v", report, readErr)
				}
				requireNativeHealthMembers(t, report, append(slices.Clone(test.members), test.rejected)...)
			}
			if fixture.count("create") != 1 || fixture.count("destroy") != 0 || fixture.count("set") != 2 || fixture.count("add") != len(test.members)+4 ||
				!slices.Equal(fixture.masks, []godcgm.HealthSystem{godcgm.HealthSystem(test.enabled), godcgm.HealthSystem(test.requested)}) || len(first.GetSystemErrors()) == 0 {
				t.Fatalf("recovery repeated setup or mutated earlier report: calls=%v masks=%v", fixture.calls, fixture.masks)
			}
		})
	}
}

func TestNativeHealthMembershipErrorsPreserveEveryRejectedID(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.entities[godcgm.FE_GPU] = []uint{2, 70, 90}
	fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 70}] = &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}
	fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 90}] = &godcgm.Error{Code: godcgm.DCGM_ST_NO_PERMISSION}
	watch, err := startHealthWatch("multiple-rejections", dcgmhealth.SystemMemory|dcgmhealth.SystemPCIe, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	report, err := watch.Read()
	if err != nil || len(report.GetSystemErrors()) != 2 || report.GetSystemsWithoutTargets() != 0 || fixture.count("check") != 1 {
		t.Fatalf("multiple rejected members: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
	requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2})
	for system, failure := range report.GetSystemErrors() {
		for _, detail := range []string{"70", "90", "DCGM_ST_BADPARAM", "DCGM_ST_NO_PERMISSION"} {
			if !strings.Contains(failure.GetDetail(), detail) {
				t.Fatalf("system %#x lost rejection %q: %v", system, detail, failure)
			}
		}
	}
}

func TestNativeHealthAllMembersRejectedFailsAndCleansGroup(t *testing.T) {
	fixture := newNativeHealthFixture()
	gpuErr, switchErr := errors.New("GPU 7 rejected"), errors.New("switch 12 rejected")
	fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}] = gpuErr
	fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12}] = switchErr
	watch, err := startHealthWatch("all-members-rejected", dcgmhealth.SystemMemory|dcgmhealth.NVSwitchSystems, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, gpuErr) || !errors.Is(readErr, switchErr) {
		t.Fatalf("all rejected members returned no-target or healthy report: report=%v err=%v", report, readErr)
	}
	native := watch.(*nativeHealthWatch)
	if fixture.count("add") != 2 || fixture.count("set") != 0 || fixture.count("check") != 0 || fixture.count("destroy") != 1 ||
		native.owned || native.configured || !native.monitoredSince.IsZero() {
		t.Fatalf("empty rejected group retained configuration: watch=%+v calls=%v", native, fixture.calls)
	}
}

func TestNativeHealthMemberDisappearsBeforeFirstSetup(t *testing.T) {
	fixture := newNativeHealthFixture()
	ops := fixture.operations()
	readInfo := ops.info
	var infoCalls int
	ops.info = func(group godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
		infoCalls++
		if infoCalls == 1 {
			// The add succeeded, but DCGM removed the GPU before verification.
			fixture.members = nil
		}
		return readInfo(group)
	}
	watch, err := startHealthWatch("disappears-before-setup", dcgmhealth.SystemMemory, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	report, err := watch.Read()
	if err != nil || report.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory || report.GroupId != nil ||
		report.MonitoredSince != nil || report.NativeGroupOverallHealth != nil || len(report.GetEntities()) != 0 || len(report.GetSystemErrors()) != 0 {
		t.Fatalf("disappearing GPU returned usable or unknown coverage: report=%v err=%v", report, err)
	}
	native := watch.(*nativeHealthWatch)
	if fixture.count("create") != 1 || fixture.count("add") != 1 || fixture.count("destroy") != 1 ||
		fixture.count("set") != 0 || fixture.count("check") != 0 || native.owned || native.configured || !native.monitoredSince.IsZero() {
		t.Fatalf("empty initial group was configured or retained: watch=%+v calls=%v", native, fixture.calls)
	}
	recovered, err := watch.Read()
	if err != nil || recovered.GetGroupId() != 2 || recovered.GetSystemsWithoutTargets() != 0 || recovered.NativeGroupOverallHealth == nil ||
		recovered.MonitoredSince == nil || fixture.count("create") != 2 || fixture.count("add") != 2 || fixture.count("destroy") != 1 ||
		fixture.count("set") != 1 || fixture.count("check") != 1 || report.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory {
		t.Fatalf("GPU did not recover after disappearing during setup: report=%v err=%v calls=%v", recovered, err, fixture.calls)
	}
	requireNativeHealthMembers(t, recovered, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7})
}

func TestNativeHealthAmbiguousAdditionUsesConfirmedMembership(t *testing.T) {
	fixture := newNativeHealthFixture()
	member := godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}
	fixture.addErrors[member] = &godcgm.Error{Code: godcgm.DCGM_ST_TIMEOUT}
	fixture.committedAdds[member] = true
	watch, err := startHealthWatch("ambiguous-add", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	for range 2 {
		report, readErr := watch.Read()
		if readErr != nil || len(report.GetSystemErrors()) != 0 || report.NativeGroupOverallHealth == nil || report.GetMonitoredSince() == nil || report.GetSystemsWithoutTargets() != 0 {
			t.Fatalf("committed addition remained unavailable: report=%v err=%v", report, readErr)
		}
		requireNativeHealthMembers(t, report, member)
	}
	if fixture.count("add") != 1 || fixture.count("set") != 1 || fixture.count("create") != 1 || fixture.count("destroy") != 0 || fixture.count("check") != 2 {
		t.Fatalf("confirmed member was duplicated or reconfigured: %v", fixture.calls)
	}
}

func TestNativeHealthAdditionSessionFailuresAbortCoverage(t *testing.T) {
	for _, failure := range []*godcgm.Error{{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}, {Code: godcgm.DCGM_ST_NOT_CONFIGURED}} {
		t.Run(dcgmStatusName(int(failure.Code)), func(t *testing.T) {
			fixture := newNativeHealthFixture()
			watch, err := startHealthWatch("invalid-add-session", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if _, err := watch.Read(); err != nil {
				t.Fatal(err)
			}
			fixture.entities[godcgm.FE_GPU] = []uint{7, 9}
			fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 9}] = failure
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
				t.Fatalf("invalid session returned partial findings: report=%v err=%v", report, readErr)
			}
			if fixture.count("check") != 1 || fixture.count("set") != 1 {
				t.Fatalf("invalid session continued health setup/read: %v", fixture.calls)
			}
		})
	}
}

func TestNativeHealthMembershipVerificationFailureRollsBack(t *testing.T) {
	fixture := newNativeHealthFixture()
	ops := fixture.operations()
	readInfo := ops.info
	failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
	var infoCalls int
	var failVerification bool
	ops.info = func(group godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
		infoCalls++
		if failVerification && infoCalls == 2 {
			return nil, failure
		}
		return readInfo(group)
	}
	watch, err := startHealthWatch("unverified-add", dcgmhealth.SystemMemory, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if _, readErr := watch.Read(); readErr != nil {
		t.Fatal(readErr)
	}
	fixture.entities[godcgm.FE_GPU] = []uint{7, 9}
	infoCalls, failVerification = 0, true
	if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
		t.Fatalf("unverified membership returned findings: report=%v err=%v", report, readErr)
	}
	if fixture.count("destroy") != 1 || fixture.count("set") != 1 || fixture.count("check") != 1 || watch.(*nativeHealthWatch).owned {
		t.Fatalf("unverified new members retained configured group: %v", fixture.calls)
	}
	failVerification = false
	report, err := watch.Read()
	if err != nil || report.GetGroupId() != 2 || len(report.GetEntities()) != 2 || fixture.count("set") != 2 || fixture.count("check") != 2 {
		t.Fatalf("unverified membership did not recover: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
}

func TestNativeHealthSetFailureAfterPartialAdditionRollsBack(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.entities[godcgm.FE_GPU] = []uint{2, 7}
	fixture.addErrors[godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}] = &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}
	failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
	fixture.failures["set"] = failure
	watch, err := startHealthWatch("partial-add-set-failure", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
		t.Fatalf("failed configuration returned partial findings: report=%v err=%v", report, readErr)
	}
	if fixture.count("add") != 2 || fixture.count("set") != 1 || fixture.count("destroy") != 1 || fixture.count("check") != 0 || watch.(*nativeHealthWatch).owned {
		t.Fatalf("failed configuration retained partially added group: %v", fixture.calls)
	}
}

func TestNativeHealthRejectedReturningGPUPreservesExistingGroup(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.entities[godcgm.FE_GPU] = []uint{2, 7}
	watch, err := startHealthWatch("rejected-returning-GPU", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Read()
	if err != nil {
		t.Fatal(err)
	}
	returning := godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}
	fixture.members = []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU, EntityId: 2}}
	fixture.addErrors[returning] = &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}
	for range 2 {
		report, readErr := watch.Read()
		if readErr != nil || report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) ||
			len(report.GetSystemErrors()) != 1 || report.GetSystemsWithoutTargets() != 0 || report.NativeGroupOverallHealth == nil {
			t.Fatalf("rejected returning GPU interrupted established group: report=%v err=%v", report, readErr)
		}
		requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2})
	}
	if fixture.count("create") != 1 || fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.count("check") != 3 {
		t.Fatalf("failed repair rolled back established coverage: %v", fixture.calls)
	}
	delete(fixture.addErrors, returning)
	report, err := watch.Read()
	if err != nil || report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) ||
		len(report.GetSystemErrors()) != 0 || fixture.count("set") != 2 || fixture.count("create") != 1 || fixture.count("destroy") != 0 {
		t.Fatalf("returning GPU failed to rejoin established group: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
	requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2}, returning)
}

func TestNativeHealthAlreadyAbsentRollbackGroupAllowsRetry(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending=%t", pending), func(t *testing.T) {
			fixture := newNativeHealthFixture()
			failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			fixture.failures["set"] = failure
			fixture.failures["destroy"] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_CONFIGURED}
			if pending {
				fixture.failures["destroy"] = &godcgm.Error{Code: godcgm.DCGM_ST_NO_PERMISSION}
			}
			watch, err := startHealthWatch("absent-rollback-group", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
				t.Fatalf("setup failure: report=%v err=%v", report, readErr)
			}
			if watch.(*nativeHealthWatch).owned != pending {
				t.Fatalf("rollback ownership=%t, pending=%t", watch.(*nativeHealthWatch).owned, pending)
			}
			delete(fixture.failures, "set")
			fixture.failures["destroy"] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_CONFIGURED}
			report, err := watch.Read()
			wantDestroy := 1
			if pending {
				wantDestroy++
			}
			if err != nil || report.GetGroupId() != 2 || report.NativeGroupOverallHealth == nil || report.MonitoredSince == nil ||
				fixture.count("create") != 2 || fixture.count("destroy") != wantDestroy || fixture.count("check") != 1 {
				t.Fatalf("absent rollback group prevented retry: report=%v err=%v calls=%v", report, err, fixture.calls)
			}
		})
	}
}

func TestNativeHealthCloseAcceptsAlreadyAbsentGroup(t *testing.T) {
	fixture := newNativeHealthFixture()
	watch, err := startHealthWatch("absent-close-group", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Read(); err != nil {
		t.Fatal(err)
	}
	fixture.failures["destroy"] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_CONFIGURED}
	for range 2 {
		if err := watch.Close(); err != nil {
			t.Fatalf("already absent group failed cleanup: %v", err)
		}
	}
	if watch.(*nativeHealthWatch).owned || fixture.count("destroy") != 1 {
		t.Fatalf("absent group retained ownership or repeated destruction: %v", fixture.calls)
	}
}

func TestNativeHealthRestoresGPUMembershipAfterSeveralAbsentReads(t *testing.T) {
	for _, returningID := range []uint{7, 9} {
		t.Run(fmt.Sprintf("returning ID %d", returningID), func(t *testing.T) {
			fixture := newNativeHealthFixture()
			fixture.entities[godcgm.FE_GPU] = []uint{2, 7}
			fixture.response = godcgm.HealthResponse{OverallHealth: godcgm.DCGM_HEALTH_RESULT_FAIL, Incidents: []godcgm.Incident{{
				System: godcgm.DCGM_HEALTH_WATCH_MEM, Health: godcgm.DCGM_HEALTH_RESULT_FAIL,
				EntityInfo: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2},
				Error:      godcgm.DiagErrorDetail{Code: godcgm.HealthCheckErrorCode(37), Message: "existing GPU fault"},
			}}}
			const requested = dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal
			watch, err := startHealthWatch("returning-GPU", requested, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			first, err := watch.Read()
			if err != nil {
				t.Fatal(err)
			}
			gpu := godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2}
			switchMember := godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12}
			fixture.entities[godcgm.FE_GPU] = []uint{2}
			fixture.members = []godcgm.GroupEntityPair{gpu, switchMember}
			for range 3 {
				report, readErr := watch.Read()
				if readErr != nil || report.GetGroupId() != first.GetGroupId() ||
					!report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) || report.GetSystemsWithoutTargets() != 0 ||
					len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 37 {
					t.Fatalf("partial absence interrupted existing coverage: report=%v err=%v", report, readErr)
				}
				requireNativeHealthMembers(t, report, gpu, switchMember)
			}
			if fixture.count("create") != 1 || fixture.count("add") != 3 || fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.count("info") != 4 {
				t.Fatalf("partial absence repeated group setup or membership reads: %v", fixture.calls)
			}
			fixture.entities[godcgm.FE_GPU] = []uint{2, returningID}
			for range 2 {
				report, readErr := watch.Read()
				if readErr != nil || report.GetGroupId() != first.GetGroupId() ||
					!report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) || report.GetSystemsWithoutTargets() != 0 {
					t.Fatalf("returning GPU changed group lifetime or was not restored: report=%v err=%v", report, readErr)
				}
				requireNativeHealthMembers(t, report, gpu, switchMember, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: returningID})
			}
			if fixture.count("create") != 1 || fixture.count("add") != 4 || fixture.count("set") != 2 || fixture.count("destroy") != 0 || fixture.count("check") != 6 ||
				fixture.discoveryCalls[godcgm.FE_GPU] != 6 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 6 {
				t.Fatalf("membership repair repeated setup or skipped fresh discovery: %v", fixture.calls)
			}
			if len(first.GetEntities()) != 3 {
				t.Fatal("membership repair mutated an earlier report")
			}
		})
	}
}

func TestNativeHealthRestoresAnEmptyOwnedGroup(t *testing.T) {
	fixture := newNativeHealthFixture()
	watch, err := startHealthWatch("empty-owned-group", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Read()
	if err != nil {
		t.Fatal(err)
	}
	fixture.entities[godcgm.FE_GPU], fixture.members = nil, nil
	for range 3 {
		report, readErr := watch.Read()
		if readErr != nil || report.GetGroupId() != first.GetGroupId() || report.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory ||
			len(report.GetEntities()) != 0 || report.NativeGroupOverallHealth != nil || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) {
			t.Fatalf("empty owned group report=%v err=%v", report, readErr)
		}
	}
	if fixture.count("create") != 1 || fixture.count("set") != 1 || fixture.count("check") != 1 || fixture.count("destroy") != 0 {
		t.Fatalf("empty group was recreated or checked: %v", fixture.calls)
	}
	fixture.entities[godcgm.FE_GPU] = []uint{7}
	report, err := watch.Read()
	if err != nil || report.GetGroupId() != first.GetGroupId() || report.NativeGroupOverallHealth == nil || report.GetSystemsWithoutTargets() != 0 ||
		!report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) {
		t.Fatalf("empty group did not recover: report=%v err=%v", report, err)
	}
	requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7})
	if fixture.count("create") != 1 || fixture.count("add") != 2 || fixture.count("set") != 2 || fixture.count("check") != 2 || fixture.discoveryCalls[godcgm.FE_GPU] != 5 {
		t.Fatalf("returning GPU did not reuse the empty group: %v", fixture.calls)
	}
}

func TestNativeHealthMembershipIncludesEntityKind(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.entities[godcgm.FE_SWITCH] = []uint{7}
	watch, err := startHealthWatch("shared-native-ID", dcgmhealth.SystemMemory|dcgmhealth.SystemNVSwitchFatal, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Read()
	if err != nil {
		t.Fatal(err)
	}
	switchMember := godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 7}
	fixture.members = []godcgm.GroupEntityPair{switchMember}
	report, err := watch.Read()
	if err != nil || report.GetGroupId() != first.GetGroupId() || fixture.count("create") != 1 || fixture.count("add") != 3 || fixture.count("set") != 2 {
		t.Fatalf("GPU and switch with the same ID were confused: report=%v err=%v calls=%v", report, err, fixture.calls)
	}
	requireNativeHealthMembers(t, report, switchMember, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7})
}

func TestNativeHealthMembershipRepairSetFailureRebuildsGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newNativeHealthFixture()
		fixture.entities[godcgm.FE_GPU] = []uint{2, 7}
		watch, err := startHealthWatch("repair-failure", dcgmhealth.SystemMemory, fixture.operations())
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		first, err := watch.Read()
		if err != nil {
			t.Fatal(err)
		}
		fixture.members = []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU, EntityId: 2}}
		failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
		fixture.failures["set"] = failure
		if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
			t.Fatalf("repair failure did not roll back: report=%v err=%v", report, readErr)
		}
		if fixture.count("destroy") != 1 || fixture.count("create") != 1 || fixture.count("check") != 1 || watch.(*nativeHealthWatch).owned {
			t.Fatalf("repair failure retained a readable group: %v", fixture.calls)
		}
		delete(fixture.failures, "set")
		time.Sleep(time.Second)
		report, err := watch.Read()
		if err != nil || report.GetGroupId() == first.GetGroupId() || !report.GetMonitoredSince().AsTime().After(first.GetMonitoredSince().AsTime()) ||
			fixture.count("create") != 2 || fixture.count("destroy") != 1 || fixture.count("check") != 2 || fixture.discoveryCalls[godcgm.FE_GPU] != 3 {
			t.Fatalf("membership repair did not recover after rollback: report=%v err=%v calls=%v", report, err, fixture.calls)
		}
		requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 2}, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7})
	})
}

func TestNativeHealthDiscoveryFailureRetainsConfiguredMembersAndIncidents(t *testing.T) {
	fixture := newNativeHealthFixture()
	fixture.response = godcgm.HealthResponse{OverallHealth: godcgm.DCGM_HEALTH_RESULT_FAIL, Incidents: []godcgm.Incident{{
		System: godcgm.DCGM_HEALTH_WATCH_MEM, Health: godcgm.DCGM_HEALTH_RESULT_FAIL,
		EntityInfo: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7},
		Error:      godcgm.DiagErrorDetail{Code: godcgm.HealthCheckErrorCode(37)},
	}}}
	const requested = dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal
	watch, err := startHealthWatch("existing-discovery-error", requested, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	first, err := watch.Read()
	if err != nil {
		t.Fatal(err)
	}
	fixture.discoveryErrors[godcgm.FE_SWITCH] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_SUPPORTED}
	report, err := watch.Read()
	if err != nil || report.GetGroupId() != first.GetGroupId() || report.GetSystemsWithoutTargets() != 0 ||
		len(report.GetSystemErrors()) != 1 || report.GetSystemErrors()[dcgmhealth.SystemNVSwitchFatal].GetSourceError() != "DCGM_ST_NOT_SUPPORTED" ||
		len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 37 || report.NativeGroupOverallHealth == nil {
		t.Fatalf("discovery failure discarded configured members or findings: report=%v err=%v", report, err)
	}
	requireNativeHealthMembers(t, report, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7}, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12})
	delete(fixture.discoveryErrors, godcgm.FE_SWITCH)
	recovered, err := watch.Read()
	if err != nil || len(recovered.GetSystemErrors()) != 0 || len(report.GetSystemErrors()) != 1 || len(first.GetSystemErrors()) != 0 ||
		fixture.count("create") != 1 || fixture.count("add") != 2 || fixture.count("set") != 1 || fixture.count("check") != 3 ||
		fixture.discoveryCalls[godcgm.FE_GPU] != 3 || fixture.discoveryCalls[godcgm.FE_SWITCH] != 3 {
		t.Fatalf("transient discovery failure reconfigured the existing group: report=%v err=%v calls=%v", recovered, err, fixture.calls)
	}
}

func TestNativeHealthAllDiscoveryFailuresRetainConfiguredCoverage(t *testing.T) {
	for _, test := range []struct {
		name      string
		requested uint64
	}{
		{name: "GPU only", requested: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe},
		{name: "GPU and switches", requested: dcgmhealth.SystemMemory | dcgmhealth.NVSwitchSystems},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			fixture.response = godcgm.HealthResponse{OverallHealth: godcgm.DCGM_HEALTH_RESULT_FAIL, Incidents: []godcgm.Incident{{
				System: godcgm.DCGM_HEALTH_WATCH_MEM, Health: godcgm.DCGM_HEALTH_RESULT_FAIL,
				EntityInfo: godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7},
				Error:      godcgm.DiagErrorDetail{Code: godcgm.HealthCheckErrorCode(37)},
			}}}
			watch, err := startHealthWatch("established-discovery-failure", test.requested, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			first, err := watch.Read()
			if err != nil {
				t.Fatal(err)
			}
			fixture.discoveryErrors[godcgm.FE_GPU] = &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			fixture.discoveryErrors[godcgm.FE_SWITCH] = &godcgm.Error{Code: godcgm.DCGM_ST_NOT_SUPPORTED}
			report, err := watch.Read()
			if err != nil || report.GetGroupId() != first.GetGroupId() || !report.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) ||
				report.GetSystemsWithoutTargets() != 0 || report.NativeGroupOverallHealth == nil || report.GetNativeGroupOverallHealth() != observation.DCGMHealthResultFail ||
				len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 37 {
				t.Fatalf("all discovery failures discarded known findings: report=%v err=%v", report, err)
			}
			members := []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU, EntityId: 7}}
			if test.requested&dcgmhealth.NVSwitchSystems != 0 {
				members = append(members, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_SWITCH, EntityId: 12})
			}
			requireNativeHealthMembers(t, report, members...)
			var unavailable uint64
			for system, failure := range report.GetSystemErrors() {
				unavailable |= system
				wantStatus := "DCGM_ST_NO_DATA"
				if system&dcgmhealth.NVSwitchSystems != 0 {
					wantStatus = "DCGM_ST_NOT_SUPPORTED"
				}
				if system == 0 || system&(system-1) != 0 || failure.GetSource() != Name || failure.GetOperation() != "health discovery" || failure.GetSourceError() != wantStatus {
					t.Fatalf("discovery failure for system %#x = %v, want %s", system, failure, wantStatus)
				}
			}
			if unavailable != test.requested {
				t.Fatalf("unavailable systems = %#x, want %#x", unavailable, test.requested)
			}
			if fixture.count("create") != 1 || fixture.count("add") != len(members) || fixture.count("set") != 1 || fixture.count("destroy") != 0 ||
				fixture.count("check") != 2 || fixture.count("info") != 2 {
				t.Fatalf("discovery failure reconfigured established coverage: %v", fixture.calls)
			}
			clear(fixture.discoveryErrors)
			recovered, err := watch.Read()
			if err != nil || len(recovered.GetSystemErrors()) != 0 || recovered.GetGroupId() != first.GetGroupId() ||
				!recovered.GetMonitoredSince().AsTime().Equal(first.GetMonitoredSince().AsTime()) || len(recovered.GetIncidents()) != 1 ||
				len(report.GetSystemErrors()) == 0 || len(first.GetSystemErrors()) != 0 || fixture.count("create") != 1 || fixture.count("add") != len(members) ||
				fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.count("check") != 3 || fixture.discoveryCalls[godcgm.FE_GPU] != 3 {
				t.Fatalf("discovery recovery restarted checks or mutated earlier reports: report=%v err=%v calls=%v", recovered, err, fixture.calls)
			}
			requireNativeHealthMembers(t, recovered, members...)
			wantSwitchDiscoveries := 0
			if test.requested&dcgmhealth.NVSwitchSystems != 0 {
				wantSwitchDiscoveries = 3
			}
			if fixture.discoveryCalls[godcgm.FE_SWITCH] != wantSwitchDiscoveries {
				t.Fatalf("switch discovery calls = %d, want %d", fixture.discoveryCalls[godcgm.FE_SWITCH], wantSwitchDiscoveries)
			}
		})
	}
}

func TestNativeHealthAllDiscoveryFailuresWithEmptyGroupReturnError(t *testing.T) {
	fixture := newNativeHealthFixture()
	watch, err := startHealthWatch("empty-established-discovery-failure", dcgmhealth.SystemMemory, fixture.operations())
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if _, readErr := watch.Read(); readErr != nil {
		t.Fatal(readErr)
	}
	fixture.members = nil
	failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
	fixture.discoveryErrors[godcgm.FE_GPU] = failure
	if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
		t.Fatalf("unknown availability returned no-target report: report=%v err=%v", report, readErr)
	}
	if fixture.count("create") != 1 || fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.count("check") != 1 {
		t.Fatalf("failed discovery changed or checked empty established group: %v", fixture.calls)
	}
}

func TestNativeHealthAbsentEstablishedGroupRebuildsOnNextRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newNativeHealthFixture()
		watch, err := startHealthWatch("absent-established-group", dcgmhealth.SystemMemory, fixture.operations())
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		first, err := watch.Read()
		if err != nil {
			t.Fatal(err)
		}
		failure := &godcgm.Error{Code: godcgm.DCGM_ST_NOT_CONFIGURED}
		fixture.failures["info"], fixture.failures["destroy"] = failure, failure
		if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
			t.Fatalf("missing group returned findings: report=%v err=%v", report, readErr)
		}
		native := watch.(*nativeHealthWatch)
		if native.owned || native.configured || !native.monitoredSince.IsZero() || fixture.count("create") != 1 || fixture.count("destroy") != 1 ||
			fixture.count("set") != 1 || fixture.count("check") != 1 {
			t.Fatalf("confirmed absent group retained setup: watch=%+v calls=%v", native, fixture.calls)
		}
		delete(fixture.failures, "info")
		delete(fixture.failures, "destroy")
		time.Sleep(time.Second)
		recovered, err := watch.Read()
		if err != nil || recovered.GetGroupId() == first.GetGroupId() || !recovered.GetMonitoredSince().AsTime().After(first.GetMonitoredSince().AsTime()) ||
			recovered.NativeGroupOverallHealth == nil || len(recovered.GetSystemErrors()) != 0 || recovered.GetSystemsWithoutTargets() != 0 ||
			fixture.count("create") != 2 || fixture.count("destroy") != 1 || fixture.count("set") != 2 || fixture.count("check") != 2 {
			t.Fatalf("confirmed absent group did not rebuild: report=%v err=%v calls=%v", recovered, err, fixture.calls)
		}
		requireNativeHealthMembers(t, recovered, godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: 7})
	})
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
	for _, established := range []bool{false, true} {
		t.Run(fmt.Sprintf("established=%t", established), func(t *testing.T) {
			fixture := newNativeHealthFixture()
			watch, err := startHealthWatch("connection-failure", dcgmhealth.KnownSystems, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			wantCalls := 0
			if established {
				if _, readErr := watch.Read(); readErr != nil {
					t.Fatal(readErr)
				}
				wantCalls = 1
			}
			fixture.discoveryErrors[godcgm.FE_SWITCH] = &godcgm.Error{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, errConnectionNotValid) {
				t.Fatalf("connection failure returned partial findings: report=%v err=%v", report, readErr)
			}
			if fixture.count("create") != wantCalls || fixture.count("set") != wantCalls || fixture.count("check") != wantCalls {
				t.Fatalf("connection failure continued native setup: %v", fixture.calls)
			}
		})
	}
}

func TestNativeHealthReconfigurationSetFailureRebuildsGroup(t *testing.T) {
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
		fixture.failures["set"] = failure
		if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
			t.Fatalf("reconfiguration failure: report=%v err=%v", report, readErr)
		}
		if fixture.count("destroy") != 1 || fixture.count("create") != 1 || watch.(*nativeHealthWatch).owned {
			t.Fatalf("failed reconfiguration retained a usable group: %v", fixture.calls)
		}
		delete(fixture.failures, "set")
		time.Sleep(time.Second)
		report, err := watch.Read()
		if err != nil || report.GetGroupId() == first.GetGroupId() || len(report.GetEntities()) != 2 || len(report.GetSystemErrors()) != 0 ||
			report.GetSystemsWithoutTargets() != 0 || !report.GetMonitoredSince().AsTime().After(first.GetMonitoredSince().AsTime()) {
			t.Fatalf("group was not rebuilt: report=%v err=%v", report, err)
		}
		if fixture.count("create") != 2 || fixture.discoveryCalls[godcgm.FE_GPU] != 3 {
			t.Fatalf("rebuild reused stale setup: calls=%v discoveries=%v", fixture.calls, fixture.discoveryCalls)
		}
	})
}

func TestNativeHealthQueryFailureKeepsConfiguredGroup(t *testing.T) {
	for _, operation := range []string{"info", "check"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newNativeHealthFixture()
			failure := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			watch, err := startHealthWatch("query-failure", dcgmhealth.SystemMemory, fixture.operations())
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if _, readErr := watch.Read(); readErr != nil {
				t.Fatal(readErr)
			}
			fixture.failures[operation] = failure
			if report, readErr := watch.Read(); report != nil || !errors.Is(readErr, failure) {
				t.Fatalf("query failure: report=%v err=%v", report, readErr)
			}
			native := watch.(*nativeHealthWatch)
			monitoredSince := native.monitoredSince
			delete(fixture.failures, operation)
			report, err := watch.Read()
			if err != nil || report.GetGroupId() != uint64(native.group.GetHandle()) || !report.GetMonitoredSince().AsTime().Equal(monitoredSince) ||
				fixture.count("create") != 1 || fixture.count("set") != 1 || fixture.count("destroy") != 0 || fixture.discoveryCalls[godcgm.FE_GPU] != 3 {
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
		ops: healthWatchOperations{
			entities: func(godcgm.Field_Entity_Group) ([]uint, error) { return []uint{0}, nil },
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
