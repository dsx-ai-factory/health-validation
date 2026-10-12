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

type interconnectDevice struct {
	device.Device
	stateReturns   map[int]nvml.Return
	counterReturns map[nvml.NvLinkErrorCounter]nvml.Return
	states         []int
	counterCalls   []int
	links          int
	fabricCalls    int
	info           nvml.GpuFabricInfo_v3
	baseRet        nvml.Return
	healthRet      nvml.Return
}

func (d *interconnectDevice) GetNvLinkState(link int) (nvml.EnableState, nvml.Return) {
	d.states = append(d.states, link)
	if ret, ok := d.stateReturns[link]; ok {
		return nvml.FEATURE_ENABLED, ret
	}
	if link >= d.links {
		return nvml.FEATURE_DISABLED, nvml.ERROR_NOT_SUPPORTED
	}
	if link == 0 {
		return nvml.FEATURE_DISABLED, nvml.SUCCESS
	}
	return nvml.FEATURE_ENABLED, nvml.SUCCESS
}

func (d *interconnectDevice) GetNvLinkErrorCounter(link int, counter nvml.NvLinkErrorCounter) (uint64, nvml.Return) {
	d.counterCalls = append(d.counterCalls, link)
	return math.MaxUint64, d.counterReturns[counter]
}

func (d *interconnectDevice) GetFabricInfo() (nvml.GpuFabricInfo_v3, nvml.Return, nvml.Return) {
	d.fabricCalls++
	return d.info, d.baseRet, d.healthRet
}

func interconnectBatch(ctx context.Context, dev device.Device, definitions ...collect.Signal) *observation.ObservationBatch {
	batch := &observation.ObservationBatch{}
	measureInterconnect(ctx, batch, timestamppb.Now(), observation.GPUTarget("gpu-1"), dev, selectedRaw(definitions...))
	return batch
}

func TestInterconnectRetainsFailedLinkAndUnsignedCounters(t *testing.T) {
	dev := &interconnectDevice{links: 3, stateReturns: map[int]nvml.Return{1: nvml.ERROR_NO_PERMISSION}, counterReturns: map[nvml.NvLinkErrorCounter]nvml.Return{nvml.NVLINK_ERROR_DL_RECOVERY: nvml.ERROR_TIMEOUT}}
	batch := interconnectBatch(context.Background(), dev, linkSignals...)
	require.Len(t, batch.Observations, 12)
	require.Equal(t, []int{0, 1, 2, 3}, dev.states)
	require.Equal(t, []int{0, 0, 0, 2, 2, 2}, dev.counterCalls)
	for link := range 3 {
		for _, current := range batch.Observations[link*4 : link*4+4] {
			require.Equal(t, uint32(link), current.Target.GetNvLinkEndpoint().GetLinkIndex())
			require.Equal(t, "gpu-1", current.Target.GetNvLinkEndpoint().GetGpu().GetUuid())
		}
	}
	require.Equal(t, uint64(0), batch.Observations[0].GetValue().GetUintValue())
	for _, current := range batch.Observations[4:8] {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, current.GetCollectionError().GetCategory())
		require.Equal(t, "GetNvLinkState", current.GetCollectionError().GetOperation())
	}
	for _, offset := range []int{0, 8} {
		require.NotNil(t, batch.Observations[offset+1].GetValue())
		require.Equal(t, uint64(math.MaxUint64), batch.Observations[offset+1].GetValue().GetUintValue())
		require.Nil(t, batch.Observations[offset+2].GetValue())
		require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[offset+2].GetCollectionError().GetCategory())
	}
	require.Nil(t, batch.Observations[4].GetValue())
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[4].GetCollectionError().GetCategory())
}

func TestInterconnectEnumeratesBeyondEighteenLinks(t *testing.T) {
	dev := &interconnectDevice{links: int(nvml.NVLINK_MAX_LINKS)}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkState)
	require.Len(t, batch.Observations, int(nvml.NVLINK_MAX_LINKS))
	require.Len(t, dev.states, int(nvml.NVLINK_MAX_LINKS))
	require.Empty(t, dev.counterCalls)
	last := batch.Observations[len(batch.Observations)-1]
	require.Equal(t, uint32(nvml.NVLINK_MAX_LINKS-1), last.Target.GetNvLinkEndpoint().GetLinkIndex())
	require.Greater(t, last.Target.GetNvLinkEndpoint().GetLinkIndex(), uint32(18))
}

func TestInterconnectEndpointCountersDoNotAdvertiseGPUAggregates(t *testing.T) {
	definitions := interconnectSignals()
	aggregates := collect.Signals{gpusignals.NVLinkDLReplayErrorCount, gpusignals.NVLinkDLRecoveryErrorCount, gpusignals.NVLinkDLCrcErrorCount}
	for _, aggregate := range aggregates {
		for _, definition := range definitions {
			require.NotEqual(t, aggregate.ID(), definition.ID())
		}
	}
	dev := &interconnectDevice{links: 1}
	selected := gpusignals.EndpointSignals()[1:]
	batch := interconnectBatch(context.Background(), dev, selected...)
	require.Len(t, batch.Observations, 3)
	for i, current := range batch.Observations {
		require.Equal(t, selected[i].ID(), current.SignalId)
		require.Equal(t, uint64(math.MaxUint64), current.GetValue().GetUintValue())
		require.Equal(t, uint32(0), current.Target.GetNvLinkEndpoint().GetLinkIndex())
	}
}

func TestInterconnectUnsupportedDoesNotInventLinks(t *testing.T) {
	dev := &interconnectDevice{}
	batch := interconnectBatch(context.Background(), dev, linkSignals...)
	require.Len(t, batch.Observations, len(linkSignals))
	require.Empty(t, dev.counterCalls)
	for _, current := range batch.Observations {
		require.Equal(t, "gpu-1", current.Target.GetGpu().GetUuid())
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, current.GetCollectionError().GetCategory())
	}
}

func TestInterconnectFabricRawAndTriState(t *testing.T) {
	dev := &interconnectDevice{info: nvml.GpuFabricInfo_v3{State: 3, Status: uint32(nvml.ERROR_NOT_READY), CliqueId: 42, ClusterUuid: [16]uint8{1}, HealthSummary: 2, HealthMask: 1 | 2<<2 | 3<<6}}
	batch := interconnectBatch(context.Background(), dev, fabricSignals...)
	require.Len(t, batch.Observations, len(fabricSignals))
	require.Equal(t, uint64(nvml.ERROR_NOT_READY), batch.Observations[1].GetValue().GetUintValue())
	require.Equal(t, "01000000-0000-0000-0000-000000000000", batch.Observations[3].GetValue().GetStringValue())
	require.True(t, batch.Observations[6].GetValue().GetBoolValue())
	require.False(t, batch.Observations[7].GetValue().GetBoolValue())
	require.Equal(t, observation.NoValueReasonNotSupported, batch.Observations[8].GetNoValue().GetReason())
	require.Nil(t, batch.Observations[9].GetValue())
	require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[9].GetCollectionError().GetCategory())
}

func TestInterconnectV1DoesNotFabricateHealth(t *testing.T) {
	dev := &interconnectDevice{info: nvml.GpuFabricInfo_v3{State: 3, CliqueId: 7}, healthRet: nvml.ERROR_NOT_SUPPORTED}
	batch := interconnectBatch(context.Background(), dev, fabricSignals...)
	require.Len(t, batch.Observations, len(fabricSignals))
	for _, current := range batch.Observations[:4] {
		require.NotNil(t, current.GetValue())
	}
	for _, current := range batch.Observations[4:] {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, current.GetCollectionError().GetCategory())
	}
}

func TestInterconnectSelectionAndFatalReads(t *testing.T) {
	dev := &interconnectDevice{links: 2}
	require.Empty(t, interconnectBatch(context.Background(), dev).Observations)
	require.Empty(t, dev.states)
	require.Zero(t, dev.fabricCalls)
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkFabricState)
	require.Len(t, batch.Observations, 1)
	require.Empty(t, dev.states)
	dev.stateReturns = map[int]nvml.Return{0: nvml.ERROR_GPU_IS_LOST}
	dev.fabricCalls = 0
	batch = interconnectBatch(context.Background(), dev, interconnectSignals()...)
	require.Equal(t, []int{0}, dev.states)
	require.Empty(t, dev.counterCalls)
	require.Zero(t, dev.fabricCalls)
	for _, current := range batch.Observations {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, current.GetCollectionError().GetCategory())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dev.states = nil
	batch = interconnectBatch(ctx, dev, interconnectSignals()...)
	require.Empty(t, dev.states)
	require.Len(t, batch.Observations, len(interconnectSignals()))
	for _, current := range batch.Observations {
		require.NotNil(t, current.GetCollectionError())
	}
}

func TestNativeReliabilityInterconnectNoDevices(t *testing.T) {
	sensor := New(&fakeInstance{exists: true})
	definitions := append(reliabilitySignals(), interconnectSignals()...)
	batch, err := sensor.Measure(context.Background(), collect.WithSignalID(definitions.IDs()...))
	require.NoError(t, err)
	require.Len(t, batch.Observations, len(definitions))
	for _, current := range batch.Observations {
		require.Nil(t, current.Target)
		require.Equal(t, observation.NoValueReasonNoTargets, current.GetNoValue().GetReason())
	}
}
