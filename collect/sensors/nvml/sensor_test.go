// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type fakeInstance struct {
	devices    map[string]device.Device
	initErr    error
	refreshErr error
	readErr    error
	exists     bool
	refreshed  bool
}

func (f *fakeInstance) NVMLExists() bool                  { return f.exists }
func (f *fakeInstance) Devices() map[string]device.Device { return f.devices }
func (f *fakeInstance) Read(read func(map[string]device.Device) error) error {
	if f.readErr != nil {
		return f.readErr
	}
	return read(f.devices)
}
func (f *fakeInstance) Shutdown() error  { return nil }
func (f *fakeInstance) InitError() error { return f.initErr }

func (f *fakeInstance) Refresh() error {
	f.refreshed = true
	if f.refreshErr != nil {
		f.devices = nil
		f.initErr = f.refreshErr
		return f.refreshErr
	}
	return nil
}

func TestMeasureRefresh(t *testing.T) {
	enumErr := errors.New("error getting device handle for index '4': Unknown Error")

	tests := []struct {
		inst          *fakeInstance
		name          string
		wantOperation string
		opts          []collect.OpOption
		wantCategory  observation.CollectionErrorCategory
		wantRefreshed bool
		wantNoTargets bool
	}{
		{
			name:          "default measures the instantiation-time device list",
			inst:          &fakeInstance{exists: true, devices: map[string]device.Device{}},
			wantNoTargets: true,
		},
		{
			name:          "default reports a failed instantiation-time enumeration",
			inst:          &fakeInstance{exists: true, initErr: enumErr},
			wantOperation: "GetDevices",
			wantCategory:  observation.CollectionErrorCategoryUnavailable,
		},
		{
			name:          "with refresh re-enumerates",
			inst:          &fakeInstance{exists: true, devices: map[string]device.Device{}},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantRefreshed: true,
			wantNoTargets: true,
		},
		{
			name:          "failed refresh marks every signal unavailable",
			inst:          &fakeInstance{exists: true, refreshErr: enumErr},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantRefreshed: true,
			wantOperation: "GetDevices",
			wantCategory:  observation.CollectionErrorCategoryUnavailable,
		},
		{
			name:          "nvml absent reports unsupported without refreshing",
			inst:          &fakeInstance{exists: false},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantOperation: "Init",
			wantCategory:  observation.CollectionErrorCategoryUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sensor := New(test.inst)
			batch, err := sensor.Measure(context.Background(), test.opts...)
			if err != nil {
				t.Fatalf("Measure() error = %v", err)
			}
			if test.inst.refreshed != test.wantRefreshed {
				t.Errorf("Refresh() called = %t, want %t", test.inst.refreshed, test.wantRefreshed)
			}

			if test.wantNoTargets {
				if len(batch.Observations) != len(sensor.Signals()) {
					t.Fatalf("Measure() emitted %d observations, want one per signal (%d)",
						len(batch.Observations), len(sensor.Signals()))
				}
				for _, current := range batch.Observations {
					if current.GetTarget() != nil {
						t.Errorf("signal %q: target = %v, want nil", current.GetSignalId(), current.GetTarget())
					}
					if current.GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
						t.Errorf("signal %q: reason = %v, want %v", current.GetSignalId(), current.GetNoValue().GetReason(), observation.NoValueReasonNoTargets)
					}
				}
				return
			}
			if len(batch.Observations) != len(sensor.Signals()) {
				t.Fatalf("Measure() emitted %d observations, want one per signal (%d)",
					len(batch.Observations), len(sensor.Signals()))
			}
			for _, obs := range batch.Observations {
				failure := obs.GetCollectionError()
				if failure == nil {
					t.Errorf("signal %q: got a value, want a collection error", obs.GetSignalId())
					continue
				}
				if failure.GetCategory() != test.wantCategory {
					t.Errorf("signal %q: category = %v, want %v", obs.GetSignalId(), failure.GetCategory(), test.wantCategory)
				}
				if failure.GetOperation() != test.wantOperation {
					t.Errorf("signal %q: operation = %q, want %q", obs.GetSignalId(), failure.GetOperation(), test.wantOperation)
				}
			}
		})
	}
}

func TestGPUInventoryEmptyAndLifecycleFailures(t *testing.T) {
	tests := []struct {
		inst      *fakeInstance
		name      string
		operation string
		category  observation.CollectionErrorCategory
		refresh   bool
	}{
		{name: "empty successful inventory", inst: &fakeInstance{exists: true}},
		{name: "closed instance", inst: &fakeInstance{exists: true, readErr: nvmlsource.ErrClosed}, operation: "Measure", category: observation.CollectionErrorCategoryUnavailable},
		{name: "enumeration failed", inst: &fakeInstance{exists: true, initErr: errors.New("enumeration failed")}, operation: "GetDevices", category: observation.CollectionErrorCategoryUnavailable},
		{name: "refresh failed", inst: &fakeInstance{exists: true, refreshErr: errors.New("refresh failed")}, refresh: true, operation: "GetDevices", category: observation.CollectionErrorCategoryUnavailable},
		{name: "library unavailable", inst: &fakeInstance{}, operation: "Init", category: observation.CollectionErrorCategoryUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch, err := New(test.inst).Measure(context.Background(), collect.WithSignalID(), collect.WithGPUInventory(), collect.WithRefresh(test.refresh))
			require.NoError(t, err)
			require.Empty(t, batch.Observations)
			require.NotNil(t, batch.Inventory)
			require.Len(t, batch.Inventory.GpuSnapshots, 1)
			snapshot := batch.Inventory.GpuSnapshots[0]
			require.Equal(t, "nvml", snapshot.Source)
			require.NotNil(t, snapshot.CollectedAt)
			if test.operation == "" {
				require.NotNil(t, snapshot.GetInventory())
				require.Empty(t, snapshot.GetInventory().GetEntries())
				require.Nil(t, snapshot.GetCollectionError())
			} else {
				require.Nil(t, snapshot.GetInventory())
				require.Equal(t, test.operation, snapshot.GetCollectionError().GetOperation())
				require.Equal(t, test.category, snapshot.GetCollectionError().GetCategory())
			}
			require.Equal(t, test.refresh, test.inst.refreshed)
		})
	}
}

// A concurrent successful Refresh can clear InitError between sensor reads.
type recoveringInstance struct {
	fakeInstance
	errorReads int
}

func (f *recoveringInstance) InitError() error {
	f.errorReads++
	if f.errorReads == 1 {
		return errors.New("enumeration failed")
	}
	return nil
}

func TestMeasureConcurrentRecovery(t *testing.T) {
	inst := &recoveringInstance{fakeInstance: fakeInstance{exists: true}}
	sensor := New(inst)
	batch, err := sensor.Measure(context.Background())
	require.NoError(t, err)
	require.Len(t, batch.Observations, len(sensor.Signals()))
	for _, obs := range batch.Observations {
		failure := obs.GetCollectionError()
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, failure.GetCategory())
		require.Equal(t, "enumeration failed", failure.GetDetail())
	}
	require.Equal(t, 1, inst.errorReads)
}

func TestClosedInstanceUnavailable(t *testing.T) {
	s := New(&fakeInstance{exists: true, readErr: nvmlsource.ErrClosed})
	batch, err := s.Measure(context.Background())
	require.NoError(t, err)
	require.NotEqualValues(t, 0, len(batch.GetObservations()))
	for _, obs := range batch.GetObservations() {
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, obs.GetCollectionError().GetCategory())
	}
}

// A slot whose handle or UUID cannot be read is reported under its NVML index
// without hiding the GPUs enumerated around it.
func TestMeasureUnresolvedSlotAmongHealthyGPUs(t *testing.T) {
	inst := &profilingInventoryInstance{
		fakeInstance: fakeInstance{exists: true, devices: map[string]device.Device{"GPU-0": &fakeDevice{}, "GPU-3": &fakeDevice{}}},
		records: []nvmlsource.InventoryDevice{
			{SourceIndex: 0, Device: &fakeDevice{}},
			{SourceIndex: 1, HandleReturn: nvml.ERROR_GPU_IS_LOST},
			{SourceIndex: 2, Device: &fakeDevice{}, UUIDReturn: nvml.ERROR_NO_PERMISSION, Error: errors.New("UUID unavailable")},
			{SourceIndex: 3, Device: &fakeDevice{}},
		},
	}
	selected := powerSignals()
	batch, err := New(inst).Measure(context.Background(), collect.WithSignalID(selected.IDs()...))
	require.NoError(t, err)
	require.NoError(t, observation.ValidateBatch(batch))

	failed := map[string][]string{}
	values := map[string][]string{}
	for _, obs := range batch.GetObservations() {
		gpu := obs.GetTarget().GetGpu()
		if slot := gpu.GetSourceLocalId(); slot != "" {
			require.Empty(t, gpu.GetUuid())
			failure := obs.GetCollectionError()
			require.NotNil(t, failure, "slot %s signal %s", slot, obs.GetSignalId())
			require.Equal(t, "GetDevices", failure.GetOperation())
			require.Equal(t, observation.CollectionErrorCategoryUnavailable, failure.GetCategory())
			require.Equal(t, "nvml", failure.GetSource())
			failed[slot] = append(failed[slot], obs.GetSignalId())
			continue
		}
		require.NotNil(t, obs.GetValue(), "GPU %s signal %s", gpu.GetUuid(), obs.GetSignalId())
		values[gpu.GetUuid()] = append(values[gpu.GetUuid()], obs.GetSignalId())
	}
	require.Equal(t, map[string][]string{"1": selected.IDs(), "2": selected.IDs()}, failed)
	require.Equal(t, map[string][]string{"GPU-0": selected.IDs(), "GPU-3": selected.IDs()}, values)
}
