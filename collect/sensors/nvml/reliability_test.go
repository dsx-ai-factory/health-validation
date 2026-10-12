// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"math"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type reliabilityDevice struct {
	device.Device
	calls     []string
	mode      nvml.EnableState
	modeRet   nvml.Return
	totalRet  nvml.Return
	memoryRet nvml.Return
	rowsRet   nvml.Return
	bankRet   nvml.Return
}

func (d *reliabilityDevice) GetEccMode() (nvml.EnableState, nvml.EnableState, nvml.Return) {
	d.calls = append(d.calls, "mode")
	return d.mode, nvml.FEATURE_ENABLED, d.modeRet
}

func (d *reliabilityDevice) GetTotalEccErrors(nvml.MemoryErrorType, nvml.EccCounterType) (uint64, nvml.Return) {
	d.calls = append(d.calls, "total")
	return math.MaxUint64, d.totalRet
}

func (d *reliabilityDevice) GetMemoryErrorCounter(nvml.MemoryErrorType, nvml.EccCounterType, nvml.MemoryLocation) (uint64, nvml.Return) {
	d.calls = append(d.calls, "memory")
	return math.MaxUint64, d.memoryRet
}

func (d *reliabilityDevice) GetRemappedRows() (int, int, bool, bool, nvml.Return) {
	d.calls = append(d.calls, "rows")
	return 3, 4, true, false, d.rowsRet
}

func (d *reliabilityDevice) GetRowRemapperHistogram() (nvml.RowRemapperHistogramValues, nvml.Return) {
	d.calls = append(d.calls, "banks")
	return nvml.RowRemapperHistogramValues{Max: 5, High: 4, Partial: 3, Low: 2, None: 1}, d.bankRet
}

func selectedRaw(definitions ...collect.Signal) map[string]bool {
	want := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		want[definition.ID()] = true
	}
	return want
}

func reliabilityBatch(ctx context.Context, dev device.Device, definitions ...collect.Signal) *observation.ObservationBatch {
	batch := &observation.ObservationBatch{}
	measureReliability(ctx, batch, timestamppb.Now(), observation.GPUTarget("gpu-1"), dev, selectedRaw(definitions...))
	return batch
}

func TestReliabilityDisabledECCPreservesTotals(t *testing.T) {
	dev := &reliabilityDevice{}
	batch := reliabilityBatch(context.Background(), dev, gpusignals.ECCModeCurrent, gpusignals.ECCModePending, gpusignals.ECCSingleBitAggregateTotal, gpusignals.ECCSingleBitAggregateDevice)
	require.Len(t, batch.Observations, 4)
	require.Equal(t, uint64(0), batch.Observations[0].GetValue().GetUintValue())
	require.Equal(t, uint64(1), batch.Observations[1].GetValue().GetUintValue())
	require.Equal(t, uint64(math.MaxUint64), batch.Observations[2].GetValue().GetUintValue())
	require.Equal(t, "ECC_DISABLED", batch.Observations[3].GetNoValue().GetSourceStatus())
	require.Equal(t, []string{"mode", "total"}, dev.calls)
}

func TestReliabilityFailuresAreIndependent(t *testing.T) {
	dev := &reliabilityDevice{mode: nvml.FEATURE_ENABLED, totalRet: nvml.ERROR_NO_PERMISSION, memoryRet: nvml.ERROR_NOT_SUPPORTED, rowsRet: nvml.ERROR_TIMEOUT}
	batch := reliabilityBatch(context.Background(), dev, gpusignals.ECCSingleBitAggregateTotal, gpusignals.ECCSingleBitAggregateDevice, gpusignals.CorrectableRemappedRows, gpusignals.RowRemapPending, gpusignals.RemapRowsAvailableNone)
	require.Len(t, batch.Observations, 5)
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[0].GetCollectionError().GetCategory())
	require.Equal(t, observation.CollectionErrorCategoryUnsupported, batch.Observations[1].GetCollectionError().GetCategory())
	require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[2].GetCollectionError().GetCategory())
	require.Nil(t, batch.Observations[3].GetValue())
	require.Equal(t, uint64(1), batch.Observations[4].GetValue().GetUintValue())
}

func TestReliabilityEverySignalAndSelection(t *testing.T) {
	dev := &reliabilityDevice{mode: nvml.FEATURE_ENABLED}
	batch := reliabilityBatch(context.Background(), dev, reliabilitySignals()...)
	require.Len(t, batch.Observations, len(reliabilitySignals()))
	seen := map[string]bool{}
	for _, current := range batch.Observations {
		require.NotNil(t, current.GetValue())
		require.False(t, seen[current.SignalId])
		seen[current.SignalId] = true
		require.Equal(t, "gpu-1", current.Target.GetGpu().GetUuid())
	}
	dev.calls = nil
	require.Empty(t, reliabilityBatch(context.Background(), dev).Observations)
	require.Empty(t, dev.calls)
}

func TestReliabilityLostAndCancelledStopNativeCalls(t *testing.T) {
	dev := &reliabilityDevice{totalRet: nvml.ERROR_GPU_IS_LOST}
	batch := reliabilityBatch(context.Background(), dev, gpusignals.ECCSingleBitAggregateTotal, gpusignals.CorrectableRemappedRows, gpusignals.RemapRowsAvailableNone)
	require.Equal(t, []string{"total"}, dev.calls)
	for _, current := range batch.Observations {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, current.GetCollectionError().GetCategory())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dev.calls = nil
	batch = reliabilityBatch(ctx, dev, reliabilitySignals()...)
	require.Empty(t, dev.calls)
	require.Len(t, batch.Observations, len(reliabilitySignals()))
	for _, current := range batch.Observations {
		require.NotNil(t, current.GetCollectionError())
	}
}
