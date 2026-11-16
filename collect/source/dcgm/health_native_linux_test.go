//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"reflect"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

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
			watch := &nativeHealthWatch{owned: true, ops: healthWatchOperations{
				info: func(godcgm.GroupHandle) (*godcgm.GroupInfo, error) {
					return &godcgm.GroupInfo{EntityList: []godcgm.GroupEntityPair{{EntityGroupId: godcgm.FE_GPU}}}, nil
				},
				check: func(godcgm.GroupHandle) (godcgm.HealthResponse, error) {
					return godcgm.HealthResponse{OverallHealth: test.native, Incidents: []godcgm.Incident{{Health: test.native}}}, nil
				},
			}}
			report, err := watch.Read()
			if err != nil || report.NativeGroupOverallHealth == nil || report.GetNativeGroupOverallHealth() != test.portable ||
				len(report.Incidents) != 1 || report.Incidents[0].GetNativeHealth() != test.portable {
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
		{name: "NVSwitch", mask: dcgmhealth.SystemNVSwitchFatal, kinds: []godcgm.Field_Entity_Group{godcgm.FE_SWITCH}},
		{name: "combined", mask: dcgmhealth.KnownSystems, kinds: []godcgm.Field_Entity_Group{godcgm.FE_GPU, godcgm.FE_SWITCH}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var kinds []godcgm.Field_Entity_Group
			var added []godcgm.GroupEntityPair
			var configured godcgm.HealthSystem
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
					return nil
				},
				destroy: func(godcgm.GroupHandle) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if !reflect.DeepEqual(kinds, test.kinds) || uint64(configured) != test.mask || len(added) != len(test.kinds) {
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
			if watch != nil || !errors.Is(err, setupErr) || !errors.Is(err, cleanupErr) {
				t.Fatalf("setup returned watch=%v err=%v", watch, err)
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
	if err != nil || report.NativeGroupOverallHealth != nil || report.GroupId != nil || len(report.Entities) != 0 {
		t.Fatalf("no-target report=%v err=%v", report, err)
	}
	if !reflect.DeepEqual(kinds, []godcgm.Field_Entity_Group{godcgm.FE_GPU, godcgm.FE_SWITCH}) {
		t.Fatalf("enumerated entity kinds=%v", kinds)
	}
	if err := watch.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeHealthPreservesUnknownIncidentsAndEntityIDs(t *testing.T) {
	var calls []string
	var group godcgm.GroupHandle
	group.SetHandle(42)
	watch := &nativeHealthWatch{
		group: group, systems: dcgmhealth.SystemMemory, owned: true,
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
	if incident.System != 1<<30 || incident.NativeHealth != 71 || incident.ErrorCode != 123456 ||
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
