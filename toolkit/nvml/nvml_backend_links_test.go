// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// linkFieldValues returns a GetFieldValuesFunc that fills the
// SPEED_MBPS_COMMON and LINK_COUNT entries with the given values, each
// stamped VALUE_TYPE_UNSIGNED_INT and the matching Return.
func linkFieldValues(speed, count uint32, speedRet, countRet gonvml.Return) func([]gonvml.FieldValue) gonvml.Return {
	return func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				binary.LittleEndian.PutUint32(values[i].Value[:4], speed)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(speedRet)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], count)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(countRet)
			}
		}
		return gonvml.SUCCESS
	}
}

func measureLinks(dev gonvml.Device, m *nvlink.NVLinkMeasurement) {
	if err := measureDeviceLinks(dev, m); err != nil {
		m.AddError(err, nvlink.SignalSpeedMBytesPerSec)
	}
}

func TestMeasureDeviceLinks_Healthy(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: linkFieldValues(50000, 18, gonvml.SUCCESS, gonvml.SUCCESS)}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if m.LinkCount != 18 {
		t.Errorf("LinkCount = %d, want 18", m.LinkCount)
	}
	if m.SpeedMBytesPerSec != 50000 {
		t.Errorf("SpeedMBytesPerSec = %d, want 50000", m.SpeedMBytesPerSec)
	}
}

// A failed DeviceGetFieldValues call flags both signals: neither value
// was read.
func TestMeasureDeviceLinks_CallFailureFlagsBoth(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func([]gonvml.FieldValue) gonvml.Return {
		return gonvml.ERROR_TIMEOUT
	}}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry", errs)
	}
	want := []string{nvlink.SignalSpeedMBytesPerSec, nvlink.SignalLinkCount}
	if !slices.Equal(errs[0].FailedSignals(), want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok {
		t.Fatalf("Err = %T, want *Error", errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetFieldValues" || nvmlErr.Return != gonvml.ERROR_TIMEOUT {
		t.Errorf("Err = %+v, want Op=DeviceGetFieldValues Return=ERROR_TIMEOUT", nvmlErr)
	}
}

// A per-entry speed failure flags only the speed signal; the link count
// still collects.
func TestMeasureDeviceLinks_SpeedEntryFailure(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: linkFieldValues(0, 18, gonvml.ERROR_TIMEOUT, gonvml.SUCCESS)}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if m.LinkCount != 18 {
		t.Errorf("LinkCount = %d, want 18 (count must still collect)", m.LinkCount)
	}
	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want only speed flagged", errs)
	}
}

// A per-entry count failure flags only the link-count signal; the speed
// still collects.
func TestMeasureDeviceLinks_CountEntryFailure(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: linkFieldValues(50000, 0, gonvml.SUCCESS, gonvml.ERROR_TIMEOUT)}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if m.SpeedMBytesPerSec != 50000 {
		t.Errorf("SpeedMBytesPerSec = %d, want 50000 (speed must still collect)", m.SpeedMBytesPerSec)
	}
	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalLinkCount}) {
		t.Fatalf("Errors() = %v, want only link-count flagged", errs)
	}
}

// A per-entry NOT_SUPPORTED on link count must be recorded as a collection error.
func TestMeasureDeviceLinks_NotSupportedFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: linkFieldValues(50000, 0, gonvml.SUCCESS, gonvml.ERROR_NOT_SUPPORTED)}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalLinkCount}) {
		t.Fatalf("Errors() = %v, want link-count flagged on NOT_SUPPORTED", errs)
	}
	var nvmlErr *Error
	if !errors.As(errs[0].Err(), &nvmlErr) || nvmlErr.Return != gonvml.ERROR_NOT_SUPPORTED {
		t.Errorf("underlying err = %v, want *Error with ERROR_NOT_SUPPORTED", errs[0].Err())
	}
}

// When the common speed field is Not Supported, NVLINK_GET_SPEED scoped by
// link ID supplies the fallback value. Legacy per-link field IDs remain
// unsupported, matching drivers where those IDs were removed.
func TestMeasureDeviceLinks_SpeedFallsBackToPerLink(t *testing.T) {
	var fallbackCalls int
	seenScopes := make([]bool, gonvml.NVLINK_MAX_LINKS)
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		if len(values) == gonvml.NVLINK_MAX_LINKS {
			fallbackCalls++
		}
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				if values[i].ScopeId >= uint32(len(seenScopes)) {
					t.Fatalf("NVLINK_GET_SPEED ScopeId = %d, want < %d", values[i].ScopeId, len(seenScopes))
				}
				seenScopes[values[i].ScopeId] = true
				binary.LittleEndian.PutUint32(values[i].Value[:4], 50000)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			default:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Fatalf("Errors() = %v, want none (per-link fallback should populate speed)", errs)
	}
	if m.SpeedMBytesPerSec != 50000 {
		t.Errorf("SpeedMBytesPerSec = %d, want 50000 from per-link fallback", m.SpeedMBytesPerSec)
	}
	if m.LinkCount != 18 {
		t.Errorf("LinkCount = %d, want 18", m.LinkCount)
	}
	if fallbackCalls != 1 {
		t.Errorf("fallback calls = %d, want 1", fallbackCalls)
	}
	for link, seen := range seenScopes {
		if !seen {
			t.Errorf("NVLINK_GET_SPEED scope %d was not queried", link)
		}
	}
}

// Unsupported common and per-link speed fields must flag speed while
// preserving the collected link count.
func TestMeasureDeviceLinks_SpeedFallbackUnavailableFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			default: // common and scoped per-link speed fields: Not Supported
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	if m.LinkCount != 18 {
		t.Errorf("LinkCount = %d, want 18 (count still collects)", m.LinkCount)
	}
	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want speed flagged as a measurement failure", errs)
	}
}

// A GET_SPEED call failure must not be treated as an unsupported field.
func TestMeasureDeviceLinks_SpeedFallbackCallErrorFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		if len(values) == gonvml.NVLINK_MAX_LINKS {
			return gonvml.ERROR_TIMEOUT
		}
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want only speed flagged", errs)
	}
	var nvmlErr *Error
	if !errors.As(errs[0].Err(), &nvmlErr) || nvmlErr.Return != gonvml.ERROR_TIMEOUT {
		t.Errorf("underlying err = %v, want *Error with ERROR_TIMEOUT", errs[0].Err())
	}
}

// A per-scope fallback error must not be masked by a readable sibling link.
func TestMeasureDeviceLinks_SpeedFallbackScopeErrorFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				switch values[i].ScopeId {
				case 0:
					binary.LittleEndian.PutUint32(values[i].Value[:4], 50000)
					values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
					values[i].NvmlReturn = uint32(gonvml.SUCCESS)
				case 1:
					values[i].NvmlReturn = uint32(gonvml.ERROR_GPU_IS_LOST)
				default:
					values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
				}
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want only speed flagged", errs)
	}
	var nvmlErr *Error
	if !errors.As(errs[0].Err(), &nvmlErr) || nvmlErr.Return != gonvml.ERROR_GPU_IS_LOST {
		t.Errorf("underlying err = %v, want *Error with ERROR_GPU_IS_LOST", errs[0].Err())
	}
	if m.SpeedMBytesPerSec != 0 {
		t.Errorf("SpeedMBytesPerSec = %d, want 0 (a hard error must not set a speed)", m.SpeedMBytesPerSec)
	}
}

// A malformed fallback value must flag speed even when another scope decodes
// cleanly; decoding as uint32 could silently truncate a wider payload.
func TestMeasureDeviceLinks_SpeedFallbackBadValueTypeFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				switch values[i].ScopeId {
				case 0:
					binary.LittleEndian.PutUint32(values[i].Value[:4], 50000)
					values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
					values[i].NvmlReturn = uint32(gonvml.SUCCESS)
				case 1:
					values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_LONG_LONG)
					values[i].NvmlReturn = uint32(gonvml.SUCCESS)
				default:
					values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
				}
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want only speed flagged", errs)
	}
}

// Scopes past the GPU's link count return INVALID_ARGUMENT; those are the
// shape of the hardware, not faults, so the speed still resolves from the
// valid scopes.
func TestMeasureDeviceLinks_SpeedFallbackIgnoresOutOfRangeScopes(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 4)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				if values[i].ScopeId < 4 {
					binary.LittleEndian.PutUint32(values[i].Value[:4], 50000)
					values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
					values[i].NvmlReturn = uint32(gonvml.SUCCESS)
				} else {
					values[i].NvmlReturn = uint32(gonvml.ERROR_INVALID_ARGUMENT)
				}
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Fatalf("Errors() = %v, want none (out-of-range scopes are benign)", errs)
	}
	if m.SpeedMBytesPerSec != 50000 {
		t.Errorf("SpeedMBytesPerSec = %d, want 50000 from the valid scopes", m.SpeedMBytesPerSec)
	}
}

// A slower in-range link surfaces as the reported speed: the fallback returns
// the minimum, not the maximum, so a single degraded link (here 25000 among
// 50000s) is not hidden behind its healthy peers. The check then fires on the
// speed mismatch instead of silently passing.
func TestMeasureDeviceLinks_SpeedFallbackReportsSlowestLink(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				speed := uint32(50000)
				if values[i].ScopeId == 7 {
					speed = 25000 // one degraded link
				}
				binary.LittleEndian.PutUint32(values[i].Value[:4], speed)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Fatalf("Errors() = %v, want none (all links readable)", errs)
	}
	if m.SpeedMBytesPerSec != 25000 {
		t.Errorf("SpeedMBytesPerSec = %d, want 25000 (slowest link, not the 50000 max)", m.SpeedMBytesPerSec)
	}
}

// An in-range link that reports NOT_SUPPORTED is a measurement failure even
// when its siblings read cleanly: partial coverage must not pass as if every
// link were verified.
func TestMeasureDeviceLinks_SpeedFallbackPartialNotSupportedFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				if values[i].ScopeId == 17 {
					values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED) // in-range, unreadable
				} else {
					binary.LittleEndian.PutUint32(values[i].Value[:4], 50000)
					values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
					values[i].NvmlReturn = uint32(gonvml.SUCCESS)
				}
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want speed flagged (an in-range link is unreadable)", errs)
	}
	var nvmlErr *Error
	if !errors.As(errs[0].Err(), &nvmlErr) || nvmlErr.Return != gonvml.ERROR_NOT_SUPPORTED {
		t.Errorf("underlying err = %v, want *Error with ERROR_NOT_SUPPORTED", errs[0].Err())
	}
	if m.SpeedMBytesPerSec != 0 {
		t.Errorf("SpeedMBytesPerSec = %d, want 0 (an in-range gap must not set a speed)", m.SpeedMBytesPerSec)
	}
}

// An in-range link that reports a zero speed is a measurement failure: a link
// training at no speed is a fault, not a value to average away.
func TestMeasureDeviceLinks_SpeedFallbackZeroSpeedFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_GET_SPEED:
				speed := uint32(50000)
				if values[i].ScopeId == 3 {
					speed = 0 // in-range link reporting no speed
				}
				binary.LittleEndian.PutUint32(values[i].Value[:4], speed)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want speed flagged (an in-range link reports 0)", errs)
	}
	if m.SpeedMBytesPerSec != 0 {
		t.Errorf("SpeedMBytesPerSec = %d, want 0 (a zero-speed link must fail, not set a speed)", m.SpeedMBytesPerSec)
	}
}

// A SUCCESS entry whose ValueType is not UNSIGNED_INT flags the signal:
// a byte-level uint32 decode would silently truncate a wider payload.
// The other field still collects.
func TestMeasureDeviceLinks_UnexpectedValueTypeFlagsSignal(t *testing.T) {
	dev := &mock.Device{GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
		for i := range values {
			switch values[i].FieldId {
			case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_LONG_LONG)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			case gonvml.FI_DEV_NVLINK_LINK_COUNT:
				binary.LittleEndian.PutUint32(values[i].Value[:4], 18)
				values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
				values[i].NvmlReturn = uint32(gonvml.SUCCESS)
			}
		}
		return gonvml.SUCCESS
	}}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinks(dev, &m)

	if m.LinkCount != 18 {
		t.Errorf("LinkCount = %d, want 18 (count collects despite speed type mismatch)", m.LinkCount)
	}
	errs := m.Errors()
	if len(errs) != 1 || !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalSpeedMBytesPerSec}) {
		t.Fatalf("Errors() = %v, want only speed flagged", errs)
	}
}

// MeasureNVLink wires measureDeviceLinks into its per-GPU loop: a
// healthy device's link count and speed land on the returned
// measurement. (deviceFabricInfo is stubbed NOT_SUPPORTED by the
// package init, so fabric stays a no-op here.)
func TestMeasureNVLink_CollectsLinks(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 1, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetUUIDFunc:        func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
				GetFieldValuesFunc: linkFieldValues(50000, 18, gonvml.SUCCESS, gonvml.SUCCESS),
				GetNvLinkStateFunc: allLinksPresent(18),
				GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
					return 1, gonvml.SUCCESS
				},
			}, gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureNVLink()
	if err != nil {
		t.Fatalf("MeasureNVLink: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1", len(ms))
	}
	m, ok := ms[0].(nvlink.NVLinkMeasurement)
	if !ok {
		t.Fatalf("measurement is not NVLinkMeasurement: %T", ms[0])
	}
	if m.LinkCount != 18 || m.SpeedMBytesPerSec != 50000 {
		t.Errorf("LinkCount=%d SpeedMBytesPerSec=%d, want 18 / 50000", m.LinkCount, m.SpeedMBytesPerSec)
	}
}

func TestNVLinkSpeedDiagnosisReusesStateReads(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix string
		state  gonvml.EnableState
		ret    gonvml.Return
		all    bool
	}{
		{name: "all inactive", all: true, prefix: "GPU GPU-0: NVLink speed unavailable: all 18 NVLinks are inactive: "},
		{name: "one inactive", state: gonvml.FEATURE_DISABLED, ret: gonvml.SUCCESS, prefix: "GPU GPU-0: NVLink speed unavailable: NVLink 0 is inactive: "},
		{name: "active", state: gonvml.FEATURE_ENABLED, ret: gonvml.SUCCESS},
		{name: "unreadable", ret: gonvml.ERROR_TIMEOUT},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, speedRet := range []gonvml.Return{gonvml.ERROR_NOT_SUPPORTED, gonvml.ERROR_INVALID_ARGUMENT, gonvml.SUCCESS} {
				dev := &mock.Device{
					GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
						linkFieldValues(0, 18, gonvml.ERROR_NOT_SUPPORTED, gonvml.SUCCESS)(values)
						for i := range values {
							if values[i].FieldId == gonvml.FI_DEV_NVLINK_GET_SPEED {
								values[i].NvmlReturn = uint32(speedRet)
								values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
							}
						}
						return gonvml.SUCCESS
					},
					GetNvLinkStateFunc: func(link int) (gonvml.EnableState, gonvml.Return) {
						if link >= 18 {
							return 0, gonvml.ERROR_INVALID_ARGUMENT
						}
						if test.all {
							return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
						}
						if link == 0 {
							return test.state, test.ret
						}
						return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
					},
					GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
						return 1, gonvml.SUCCESS
					},
				}
				m := (nvlinkProbe{}).Measure(ProbeContext{Self: ProbeDevice{Device: dev, UUID: "GPU-0"}}).(nvlink.NVLinkMeasurement)
				// Active-link and capability collection each read state once.
				wantCalls := 2 * gonvml.NVLINK_MAX_LINKS
				if test.ret == gonvml.ERROR_TIMEOUT {
					wantCalls = 2
				}
				if got := len(dev.GetNvLinkStateCalls()); got != wantCalls {
					t.Fatalf("%d state calls, want %d", got, wantCalls)
				}
				var speedError error
				for _, failure := range m.Errors() {
					if !slices.Contains(failure.FailedSignals(), nvlink.SignalSpeedMBytesPerSec) {
						continue
					}
					speedError = failure.Err()
				}
				var original *linkSpeedError
				if !errors.As(speedError, &original) {
					t.Fatalf("lost original speed error: %v", speedError)
				}
				if want := test.prefix + original.Error(); speedError.Error() != want {
					t.Fatalf("failure = %q, want %q", speedError, want)
				}
			}
		})
	}
}
