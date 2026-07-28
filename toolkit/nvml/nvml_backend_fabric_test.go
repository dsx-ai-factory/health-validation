/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

//go:build linux && !android

package nvml

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/health-validation/catalog/nvlink"
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

func TestMeasureDeviceFabric_HealthyMask(t *testing.T) {
	// Every field reports _FALSE — the GPU is on the fabric and all
	// health parameters are nominal.
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
		Status: uint32(gonvml.SUCCESS),
		HealthMask: fabricMask(
			gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_FALSE,
			gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE,
			gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE,
			gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_FALSE,
		),
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if m.FabricBandwidthDegraded || m.FabricRouteRecoveryInProgress ||
		m.FabricRouteUnhealthy || m.FabricAccessTimeoutRecovery {
		t.Errorf("expected all fabric signals healthy, got %+v", m)
	}
	if m.FabricRegistrationIncomplete || m.FabricRegistrationFailed {
		t.Errorf("expected healthy registration signals, got %+v", m)
	}
	if len(m.Errors()) != 0 {
		t.Errorf("Errors() = %v, want none", m.Errors())
	}
}

func TestMeasureDeviceFabric_BandwidthDegraded(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{HealthMask: fabricMask(
		gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_TRUE, 0, 0, 0,
	)}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricBandwidthDegraded {
		t.Error("FabricBandwidthDegraded = false, want true")
	}
	if m.FabricRouteRecoveryInProgress || m.FabricRouteUnhealthy || m.FabricAccessTimeoutRecovery {
		t.Errorf("only bandwidth should be flagged, got %+v", m)
	}
}

func TestMeasureDeviceFabric_RouteRecoveryInProgress(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{HealthMask: fabricMask(
		0, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_TRUE, 0, 0,
	)}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRouteRecoveryInProgress {
		t.Error("FabricRouteRecoveryInProgress = false, want true")
	}
}

func TestMeasureDeviceFabric_RouteUnhealthy(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{HealthMask: fabricMask(
		0, 0, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE, 0,
	)}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRouteUnhealthy {
		t.Error("FabricRouteUnhealthy = false, want true")
	}
}

func TestMeasureDeviceFabric_AccessTimeoutRecovery(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{HealthMask: fabricMask(
		0, 0, 0, gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_TRUE,
	)}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricAccessTimeoutRecovery {
		t.Error("FabricAccessTimeoutRecovery = false, want true")
	}
}

// ERROR_NOT_SUPPORTED means the fabric-info query is unavailable. On GB200
// that is a registration failure, so the incomplete signal is set while the
// health-mask signals stay neutral and no error is recorded.
func TestMeasureDeviceFabric_NotSupported(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{}, gonvml.ERROR_NOT_SUPPORTED)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if m.FabricBandwidthDegraded || m.FabricRouteRecoveryInProgress ||
		m.FabricRouteUnhealthy || m.FabricAccessTimeoutRecovery {
		t.Errorf("expected neutral health-mask signals on NOT_SUPPORTED, got %+v", m)
	}
	if !m.FabricRegistrationIncomplete {
		t.Error("FabricRegistrationIncomplete = false, want true on NOT_SUPPORTED")
	}
	if m.FabricRegistrationFailed {
		t.Error("FabricRegistrationFailed = true, want false on NOT_SUPPORTED")
	}
	if len(m.Errors()) != 0 {
		t.Errorf("Errors() = %v, want none on NOT_SUPPORTED", m.Errors())
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

func TestMeasureDeviceFabric_RegistrationComplete(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
		Status: uint32(gonvml.SUCCESS),
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if m.FabricRegistrationIncomplete || m.FabricRegistrationFailed {
		t.Errorf("expected healthy registration signals, got %+v", m)
	}
	if len(m.Errors()) != 0 {
		t.Errorf("Errors() = %v, want none", m.Errors())
	}
}

func TestMeasureDeviceFabric_RegistrationIncomplete(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State: gonvml.GPU_FABRIC_STATE_IN_PROGRESS,
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRegistrationIncomplete {
		t.Error("FabricRegistrationIncomplete = false, want true (state != COMPLETED)")
	}
	if m.FabricRegistrationFailed {
		t.Error("FabricRegistrationFailed = true, want false (status not evaluated until COMPLETED)")
	}
}

func TestMeasureDeviceFabric_RegistrationFailed(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State:  gonvml.GPU_FABRIC_STATE_COMPLETED,
		Status: uint32(gonvml.ERROR_TIMEOUT),
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRegistrationFailed {
		t.Error("FabricRegistrationFailed = false, want true (completed with non-success status)")
	}
	if m.FabricRegistrationIncomplete {
		t.Error("FabricRegistrationIncomplete = true, want false (state is COMPLETED)")
	}
}

// A probe that has not completed carries an undefined Status; the failed
// signal must stay false rather than fold that undefined value.
func TestMeasureDeviceFabric_RegistrationStatusIgnoredWhenIncomplete(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State:  gonvml.GPU_FABRIC_STATE_IN_PROGRESS,
		Status: uint32(gonvml.ERROR_TIMEOUT),
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRegistrationIncomplete {
		t.Error("FabricRegistrationIncomplete = false, want true")
	}
	if m.FabricRegistrationFailed {
		t.Error("FabricRegistrationFailed = true, want false (status undefined while incomplete)")
	}
}

// State NOT_SUPPORTED from a SUCCESS query means the GPU is not on the
// fabric. On GB200 that is a registration failure — the same verdict as an
// ERROR_NOT_SUPPORTED return — so the incomplete signal is set.
func TestMeasureDeviceFabric_RegistrationStateNotSupported(t *testing.T) {
	withFabricInfo(t, gonvml.GpuFabricInfo_v3{
		State: gonvml.GPU_FABRIC_STATE_NOT_SUPPORTED,
	}, gonvml.SUCCESS)

	var m nvlink.NVLinkMeasurement
	measureDeviceFabric(nil, &m)

	if !m.FabricRegistrationIncomplete {
		t.Error("FabricRegistrationIncomplete = false, want true for State=NOT_SUPPORTED")
	}
	if m.FabricRegistrationFailed {
		t.Error("FabricRegistrationFailed = true, want false for State=NOT_SUPPORTED")
	}
	if len(m.Errors()) != 0 {
		t.Errorf("Errors() = %v, want none", m.Errors())
	}
}
