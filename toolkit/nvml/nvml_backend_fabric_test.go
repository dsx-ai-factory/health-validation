// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// The real deviceFabricInfo calls into libnvidia-ml, which is absent on
// CI. Default every test in this binary to "fabric not supported" so
// any test that reaches fabric collection — directly via MeasureNVLink
// or through Collect with an NVLink sensor — never hits cgo. The fabric
// tests below override it per-case via withFabricInfo.
func init() {
	deviceFabricInfo = func(gonvml.Device) (gonvml.GpuFabricInfo_v3, gonvml.Return) {
		return gonvml.GpuFabricInfo_v3{}, gonvml.ERROR_NOT_SUPPORTED
	}
}

// withFabricInfo makes deviceFabricInfo return the given info/ret for
// the duration of the test, restoring the previous value on cleanup so
// the override does not bleed into other tests in the binary.
func withFabricInfo(t *testing.T, info gonvml.GpuFabricInfo_v3, ret gonvml.Return) {
	t.Helper()
	orig := deviceFabricInfo
	deviceFabricInfo = func(gonvml.Device) (gonvml.GpuFabricInfo_v3, gonvml.Return) {
		return info, ret
	}
	t.Cleanup(func() { deviceFabricInfo = orig })
}

// fabricMask packs the four 2-bit fabric health fields into one
// healthMask the way NVML reports it.
func fabricMask(degradedBW, routeRecovery, routeUnhealthy, accessTimeout uint32) uint32 {
	return degradedBW<<gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW |
		routeRecovery<<gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY |
		routeUnhealthy<<gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY |
		accessTimeout<<gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY
}

func TestMeasureDeviceFabric_HealthAndRegistration(t *testing.T) {
	tests := []struct {
		name                        string
		info                        gonvml.GpuFabricInfo_v3
		ret                         gonvml.Return
		wantBandwidthDegraded       bool
		wantRouteRecoveryInProgress bool
		wantRouteUnhealthy          bool
		wantAccessTimeoutRecovery   bool
		wantRegistrationIncomplete  bool
		wantRegistrationFailed      bool
	}{
		{
			// Every health-mask field reports _FALSE — the GPU is on the
			// fabric and all health parameters are nominal.
			name: "healthy mask",
			info: gonvml.GpuFabricInfo_v3{
				State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status: uint32(gonvml.SUCCESS),
				HealthMask: fabricMask(
					gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_FALSE,
					gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE,
					gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE,
					gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_FALSE,
				),
			},
			ret: gonvml.SUCCESS,
		},
		{
			name: "bandwidth degraded",
			info: gonvml.GpuFabricInfo_v3{
				State:      gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status:     uint32(gonvml.SUCCESS),
				HealthMask: fabricMask(gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_TRUE, 0, 0, 0),
			},
			ret:                   gonvml.SUCCESS,
			wantBandwidthDegraded: true,
		},
		{
			name: "route recovery in progress",
			info: gonvml.GpuFabricInfo_v3{
				State:      gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status:     uint32(gonvml.SUCCESS),
				HealthMask: fabricMask(0, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_TRUE, 0, 0),
			},
			ret:                         gonvml.SUCCESS,
			wantRouteRecoveryInProgress: true,
		},
		{
			name: "route unhealthy",
			info: gonvml.GpuFabricInfo_v3{
				State:      gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status:     uint32(gonvml.SUCCESS),
				HealthMask: fabricMask(0, 0, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE, 0),
			},
			ret:                gonvml.SUCCESS,
			wantRouteUnhealthy: true,
		},
		{
			name: "access timeout recovery",
			info: gonvml.GpuFabricInfo_v3{
				State:      gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status:     uint32(gonvml.SUCCESS),
				HealthMask: fabricMask(0, 0, 0, gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_TRUE),
			},
			ret:                       gonvml.SUCCESS,
			wantAccessTimeoutRecovery: true,
		},
		{
			// ERROR_NOT_SUPPORTED means the fabric-info query is unavailable.
			// On GB200 that is a registration failure, so the incomplete
			// signal is set while the health-mask signals stay neutral.
			name:                       "not supported",
			info:                       gonvml.GpuFabricInfo_v3{},
			ret:                        gonvml.ERROR_NOT_SUPPORTED,
			wantRegistrationIncomplete: true,
		},
		{
			name: "registration complete",
			info: gonvml.GpuFabricInfo_v3{
				State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status: uint32(gonvml.SUCCESS),
			},
			ret: gonvml.SUCCESS,
		},
		{
			name:                       "registration incomplete",
			info:                       gonvml.GpuFabricInfo_v3{State: gonvml.GPU_FABRIC_STATE_IN_PROGRESS},
			ret:                        gonvml.SUCCESS,
			wantRegistrationIncomplete: true,
		},
		{
			name: "registration failed",
			info: gonvml.GpuFabricInfo_v3{
				State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
				Status: uint32(gonvml.ERROR_TIMEOUT),
			},
			ret:                    gonvml.SUCCESS,
			wantRegistrationFailed: true,
		},
		{
			// A probe that has not completed carries an undefined Status;
			// the failed signal must stay false rather than fold that
			// undefined value.
			name: "registration status ignored when incomplete",
			info: gonvml.GpuFabricInfo_v3{
				State:  gonvml.GPU_FABRIC_STATE_IN_PROGRESS,
				Status: uint32(gonvml.ERROR_TIMEOUT),
			},
			ret:                        gonvml.SUCCESS,
			wantRegistrationIncomplete: true,
		},
		{
			// State NOT_SUPPORTED from a SUCCESS query means the GPU is not
			// on the fabric. On GB200 that is a registration failure — the
			// same verdict as an ERROR_NOT_SUPPORTED return.
			name:                       "registration state not supported",
			info:                       gonvml.GpuFabricInfo_v3{State: gonvml.GPU_FABRIC_STATE_NOT_SUPPORTED},
			ret:                        gonvml.SUCCESS,
			wantRegistrationIncomplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFabricInfo(t, tt.info, tt.ret)

			var m nvlink.NVLinkMeasurement
			measureDeviceFabric(nil, &m)

			if m.FabricBandwidthDegraded != tt.wantBandwidthDegraded {
				t.Errorf("FabricBandwidthDegraded = %v, want %v", m.FabricBandwidthDegraded, tt.wantBandwidthDegraded)
			}
			if m.FabricRouteRecoveryInProgress != tt.wantRouteRecoveryInProgress {
				t.Errorf("FabricRouteRecoveryInProgress = %v, want %v", m.FabricRouteRecoveryInProgress, tt.wantRouteRecoveryInProgress)
			}
			if m.FabricRouteUnhealthy != tt.wantRouteUnhealthy {
				t.Errorf("FabricRouteUnhealthy = %v, want %v", m.FabricRouteUnhealthy, tt.wantRouteUnhealthy)
			}
			if m.FabricAccessTimeoutRecovery != tt.wantAccessTimeoutRecovery {
				t.Errorf("FabricAccessTimeoutRecovery = %v, want %v", m.FabricAccessTimeoutRecovery, tt.wantAccessTimeoutRecovery)
			}
			if m.FabricRegistrationIncomplete != tt.wantRegistrationIncomplete {
				t.Errorf("FabricRegistrationIncomplete = %v, want %v", m.FabricRegistrationIncomplete, tt.wantRegistrationIncomplete)
			}
			if m.FabricRegistrationFailed != tt.wantRegistrationFailed {
				t.Errorf("FabricRegistrationFailed = %v, want %v", m.FabricRegistrationFailed, tt.wantRegistrationFailed)
			}
			if len(m.Errors()) != 0 {
				t.Errorf("Errors() = %v, want none", m.Errors())
			}
		})
	}
}

// A hard NVML failure records one errored entry covering all six
// fabric signals so the catalog fails the fabric check Critical.
func TestMeasureDeviceFabric_HardFailure(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{}, gonvml.ERROR_UNKNOWN)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want one entry after fabric query failure", errs)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok {
		t.Fatalf("Err = %T, want *Error", errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetGpuFabricInfoV" {
		t.Errorf("Op = %q, want DeviceGetGpuFabricInfoV", nvmlErr.Op)
	}
	want := []string{
		nvlink.SignalFabricBandwidthDegraded,
		nvlink.SignalFabricRouteRecoveryInProgress,
		nvlink.SignalFabricRouteUnhealthy,
		nvlink.SignalFabricAccessTimeoutRecovery,
		nvlink.SignalFabricRegistrationIncomplete,
		nvlink.SignalFabricRegistrationFailed,
	}
	if len(errs[0].FailedSignals()) != len(want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	for _, sig := range want {
		if !slices.Contains(errs[0].FailedSignals(), sig) {
			t.Errorf("FailedSignals missing %q (got %v)", sig, errs[0].FailedSignals())
		}
	}
}
