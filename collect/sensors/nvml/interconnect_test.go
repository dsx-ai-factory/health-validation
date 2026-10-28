// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"encoding/binary"
	"math"
	"slices"
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
	capReturns     map[nvml.NvLinkCapability]nvml.Return
	p2pStatus      map[string]nvml.GpuP2PStatus
	p2pReturns     map[string]nvml.Return
	uuid           string
	states         []int
	counterCalls   []int
	links          int
	fabricCalls    int
	fieldCalls     int
	info           nvml.GpuFabricInfo_v3
	baseRet        nvml.Return
	healthRet      nvml.Return
	fieldRet       nvml.Return
	linkCount      uint32
	speed          uint32
	linkSpeed      uint32
	wrongType      bool
	countZero      bool
}

// GetFieldValues answers link count and common speed when nonzero, and the
// per-link speed for links below links; anything else is not supported.
func (d *interconnectDevice) GetFieldValues(values []nvml.FieldValue) nvml.Return {
	d.fieldCalls++
	if d.fieldRet != nvml.SUCCESS {
		return d.fieldRet
	}
	for i := range values {
		var value uint32
		switch values[i].FieldId {
		case nvml.FI_DEV_NVLINK_LINK_COUNT:
			value = d.linkCount
			if d.countZero {
				values[i].ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_INT)
				continue
			}
		case nvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
			value = d.speed
		case nvml.FI_DEV_NVLINK_GET_SPEED:
			if int(values[i].ScopeId) < d.links {
				value = d.linkSpeed
			}
		}
		if value == 0 {
			values[i].NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			continue
		}
		values[i].ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_INT)
		if d.wrongType {
			values[i].ValueType = uint32(nvml.VALUE_TYPE_DOUBLE)
		}
		binary.LittleEndian.PutUint32(values[i].Value[:4], value)
	}
	return nvml.SUCCESS
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

// GetNvLinkCapability reports capability c of link l as 10*l+c+1, so each
// link's and capability's value is distinguishable.
func (d *interconnectDevice) GetNvLinkCapability(link int, c nvml.NvLinkCapability) (uint32, nvml.Return) {
	return uint32(10*link) + uint32(c) + 1, d.capReturns[c]
}

func (d *interconnectDevice) GetP2PStatus(peer nvml.Device, index nvml.GpuP2PCapsIndex) (nvml.GpuP2PStatus, nvml.Return) {
	if index != nvml.P2P_CAPS_INDEX_NVLINK {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	uuid := peer.(*interconnectDevice).uuid
	return d.p2pStatus[uuid], d.p2pReturns[uuid]
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
	batch := interconnectBatch(context.Background(), dev, loopSignals...)
	width := len(loopSignals)
	require.Len(t, batch.Observations, 4*width)
	require.Equal(t, []int{0, 1, 2, 3}, dev.states)
	require.Equal(t, []int{0, 0, 0, 2, 2, 2}, dev.counterCalls)
	for link := range 4 {
		for _, current := range batch.Observations[link*width : link*width+width] {
			require.Equal(t, uint32(link), current.Target.GetNvLinkEndpoint().GetLinkIndex())
			require.Equal(t, "gpu-1", current.Target.GetNvLinkEndpoint().GetGpu().GetUuid())
		}
	}
	require.Equal(t, uint64(0), batch.Observations[0].GetValue().GetUintValue())
	for _, current := range batch.Observations[width : 2*width] {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, current.GetCollectionError().GetCategory())
		require.Equal(t, "GetNvLinkState", current.GetCollectionError().GetOperation())
	}
	for _, offset := range []int{0, 2 * width} {
		require.Equal(t, uint64(math.MaxUint64), batch.Observations[offset+1].GetValue().GetUintValue())
		require.Nil(t, batch.Observations[offset+2].GetValue())
		require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[offset+2].GetCollectionError().GetCategory())
	}
	// The unsupported index past the last link is recorded and ends the loop.
	for _, current := range batch.Observations[3*width:] {
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, current.GetCollectionError().GetCategory())
	}
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
	dev := &interconnectDevice{links: 1, linkCount: 1}
	selected := collect.Signals{gpusignals.NVLinkEndpointReplayErrorCount, gpusignals.NVLinkEndpointRecoveryErrorCount, gpusignals.NVLinkEndpointCRCFlitErrorCount}
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
	for _, current := range batch.Observations[4:6] {
		require.Nil(t, current.GetValue())
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, current.GetCollectionError().GetCategory())
	}
	// The health booleans are shared with DCGM, which reports this as NoValue.
	for _, current := range batch.Observations[6:] {
		require.Equal(t, observation.NoValueReasonNotSupported, current.GetNoValue().GetReason())
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
	batch = interconnectBatch(context.Background(), dev, slices.Concat(loopSignals, fabricSignals)...)
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
	// P2P pairs are read by measurePeers, not per device.
	require.Len(t, batch.Observations, len(interconnectSignals())-len(gpusignals.PairSignals()))
	for _, current := range batch.Observations {
		require.NotNil(t, current.GetCollectionError())
	}
}

func TestInterconnectLinkCountAndCommonSpeed(t *testing.T) {
	dev := &interconnectDevice{linkCount: 18, speed: 53125}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkLinkCount, gpusignals.NVLinkSpeedCommon)
	require.Equal(t, 1, dev.fieldCalls)
	require.Len(t, batch.Observations, 2)
	for i, want := range []struct {
		definition collect.Signal
		value      uint64
	}{{gpusignals.NVLinkLinkCount, 18}, {gpusignals.NVLinkSpeedCommon, 53125}} {
		current := batch.Observations[i]
		require.Equal(t, want.definition.ID(), current.SignalId)
		require.Equal(t, *want.definition.Unit(), current.GetUnit())
		require.Equal(t, "gpu-1", current.Target.GetGpu().GetUuid())
		require.Equal(t, want.value, current.GetValue().GetUintValue())
	}
}

func TestInterconnectLinkFieldFailures(t *testing.T) {
	definitions := collect.Signals{gpusignals.NVLinkLinkCount, gpusignals.NVLinkSpeedCommon}

	batch := interconnectBatch(context.Background(), &interconnectDevice{speed: 53125}, definitions...)
	require.Len(t, batch.Observations, 2)
	require.Equal(t, observation.CollectionErrorCategoryUnsupported, batch.Observations[0].GetCollectionError().GetCategory())
	require.Equal(t, "ERROR_NOT_SUPPORTED", batch.Observations[0].GetCollectionError().GetSourceError())
	require.Equal(t, uint64(53125), batch.Observations[1].GetValue().GetUintValue())

	batch = interconnectBatch(context.Background(), &interconnectDevice{linkCount: 18, speed: 53125, wrongType: true}, definitions...)
	require.Len(t, batch.Observations, 2)
	for _, current := range batch.Observations {
		require.Equal(t, observation.CollectionErrorCategoryParseError, current.GetCollectionError().GetCategory())
	}

	batch = interconnectBatch(context.Background(), &interconnectDevice{fieldRet: nvml.ERROR_NO_PERMISSION}, definitions...)
	require.Len(t, batch.Observations, 2)
	for _, current := range batch.Observations {
		require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, current.GetCollectionError().GetCategory())
		require.Equal(t, "GetFieldValues", current.GetCollectionError().GetOperation())
	}

	dev := &interconnectDevice{links: 2, fieldRet: nvml.ERROR_GPU_IS_LOST}
	batch = interconnectBatch(context.Background(), dev, gpusignals.NVLinkLinkCount, gpusignals.NVLinkState)
	require.Empty(t, dev.states)
	require.Len(t, batch.Observations, 2)
	for _, current := range batch.Observations {
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, current.GetCollectionError().GetCategory())
	}
}

func TestInterconnectLinkCountBoundsEndpoints(t *testing.T) {
	dev := &interconnectDevice{links: int(nvml.NVLINK_MAX_LINKS), linkCount: 4}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkState)
	require.Equal(t, []int{0, 1, 2, 3}, dev.states)
	require.Len(t, batch.Observations, 4)
}

func TestInterconnectLinkCapabilities(t *testing.T) {
	capabilities := gpusignals.EndpointCapabilitySignals()
	require.Len(t, capabilities, int(nvml.NVLINK_CAP_COUNT))

	// Link 0 is disabled and still probed; link 1 fails its state read.
	dev := &interconnectDevice{links: 3, linkCount: 3, stateReturns: map[int]nvml.Return{1: nvml.ERROR_NO_PERMISSION}, capReturns: map[nvml.NvLinkCapability]nvml.Return{nvml.NVLINK_CAP_SLI_BRIDGE: nvml.ERROR_NOT_SUPPORTED}}
	batch := interconnectBatch(context.Background(), dev, capabilities...)
	require.Len(t, batch.Observations, 3*len(capabilities))
	for link := range 3 {
		for i, current := range batch.Observations[link*len(capabilities) : (link+1)*len(capabilities)] {
			require.Equal(t, capabilities[i].ID(), current.SignalId)
			require.Equal(t, uint32(link), current.Target.GetNvLinkEndpoint().GetLinkIndex())
			switch {
			case link == 1:
				require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, current.GetCollectionError().GetCategory())
				require.Equal(t, "GetNvLinkState", current.GetCollectionError().GetOperation())
			case nvml.NvLinkCapability(i) == nvml.NVLINK_CAP_SLI_BRIDGE:
				require.Equal(t, observation.CollectionErrorCategoryUnsupported, current.GetCollectionError().GetCategory())
				require.Equal(t, "GetNvLinkCapability", current.GetCollectionError().GetOperation())
			default:
				require.Equal(t, uint64(10*link+i+1), current.GetValue().GetUintValue())
			}
		}
	}
}

func TestInterconnectP2PStatusPerOrderedPair(t *testing.T) {
	measure := func(devices ...*interconnectDevice) []*observation.Observation {
		t.Helper()
		byUUID := make(map[string]device.Device, len(devices))
		for _, dev := range devices {
			byUUID[dev.uuid] = dev
		}
		batch, err := New(&fakeInstance{exists: true, devices: byUUID}).Measure(context.Background(), collect.WithSignalID(gpusignals.NVLinkP2PStatus.ID(), gpusignals.NVLinkState.ID()))
		require.NoError(t, err)
		return slices.DeleteFunc(batch.Observations, func(o *observation.Observation) bool { return o.SignalId != gpusignals.NVLinkP2PStatus.ID() })
	}

	a := &interconnectDevice{uuid: "a", p2pStatus: map[string]nvml.GpuP2PStatus{"c": nvml.P2P_STATUS_NOT_SUPPORTED}}
	b := &interconnectDevice{uuid: "b", p2pReturns: map[string]nvml.Return{"c": nvml.ERROR_UNKNOWN}}
	c := &interconnectDevice{uuid: "c"}
	pairs := measure(a, b, c)
	want := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "a"}, {"b", "c"}, {"c", "a"}, {"c", "b"}}
	require.Len(t, pairs, len(want))
	for i, pair := range want {
		current := pairs[i]
		require.Equal(t, pair[0], current.Target.GetGpuPair().GetSourceGpu().GetUuid())
		require.Equal(t, pair[1], current.Target.GetGpuPair().GetPeerGpu().GetUuid())
		switch pair {
		case [2]string{"a", "c"}:
			require.Equal(t, uint64(nvml.P2P_STATUS_NOT_SUPPORTED), current.GetValue().GetUintValue())
		case [2]string{"b", "c"}:
			require.Equal(t, "GetP2PStatus", current.GetCollectionError().GetOperation())
			require.Equal(t, "ERROR_UNKNOWN", current.GetCollectionError().GetSourceError())
		default:
			require.Equal(t, uint64(nvml.P2P_STATUS_OK), current.GetValue().GetUintValue())
		}
	}

	// A lone GPU reports that it has no peers rather than nothing.
	lone := measure(&interconnectDevice{uuid: "a"})
	require.Len(t, lone, 1)
	require.Equal(t, "a", lone[0].Target.GetGpu().GetUuid())
	require.Equal(t, observation.NoValueReasonNoTargets, lone[0].GetNoValue().GetReason())

	// A lost peer fails only its own pair; the source's other pairs are read.
	a = &interconnectDevice{uuid: "a", p2pReturns: map[string]nvml.Return{"b": nvml.ERROR_GPU_IS_LOST}}
	pairs = measure(a, &interconnectDevice{uuid: "b"}, &interconnectDevice{uuid: "c"})
	require.Equal(t, "ERROR_GPU_IS_LOST", pairs[0].GetCollectionError().GetSourceError())
	require.Equal(t, "c", pairs[1].Target.GetGpuPair().GetPeerGpu().GetUuid())
	require.Equal(t, uint64(nvml.P2P_STATUS_OK), pairs[1].GetValue().GetUintValue())

	// A lost GPU's P2P signal fails at its GPU target; it is still a peer.
	lost := &interconnectDevice{uuid: "a", stateReturns: map[int]nvml.Return{0: nvml.ERROR_GPU_IS_LOST}}
	pairs = measure(lost, &interconnectDevice{uuid: "b"})
	require.Len(t, pairs, 2)
	require.Equal(t, "a", pairs[0].Target.GetGpu().GetUuid())
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, pairs[0].GetCollectionError().GetCategory())
	require.Equal(t, "b", pairs[1].Target.GetGpuPair().GetSourceGpu().GetUuid())
	require.Equal(t, "a", pairs[1].Target.GetGpuPair().GetPeerGpu().GetUuid())
}

func TestInterconnectLinkSpeedsReadOnceWhateverState(t *testing.T) {
	dev := &interconnectDevice{links: 3, linkCount: 3, linkSpeed: 53125, stateReturns: map[int]nvml.Return{1: nvml.ERROR_NO_PERMISSION}}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkEndpointSpeed, gpusignals.NVLinkState)
	require.Equal(t, 2, dev.fieldCalls)
	speeds := slices.DeleteFunc(batch.Observations, func(o *observation.Observation) bool { return o.SignalId != gpusignals.NVLinkEndpointSpeed.ID() })
	require.Len(t, speeds, 3)
	for link, current := range speeds {
		require.Equal(t, uint32(link), current.Target.GetNvLinkEndpoint().GetLinkIndex())
		require.Equal(t, uint64(53125), current.GetValue().GetUintValue())
	}

	// Without a positive link count, per-link speed fails at the GPU and the
	// state loop still reads until NOT_SUPPORTED.
	dev = &interconnectDevice{links: 2, countZero: true, linkSpeed: 53125}
	batch = interconnectBatch(context.Background(), dev, gpusignals.NVLinkEndpointSpeed, gpusignals.NVLinkState)
	require.Equal(t, []int{0, 1, 2}, dev.states)
	speed := batch.Observations[0]
	require.Equal(t, gpusignals.NVLinkEndpointSpeed.ID(), speed.SignalId)
	require.Equal(t, "gpu-1", speed.Target.GetGpu().GetUuid())
	require.Equal(t, observation.CollectionErrorCategoryUnsupported, speed.GetCollectionError().GetCategory())
}

func TestInterconnectInRangeUnsupportedLinkFailsCapabilities(t *testing.T) {
	dev := &interconnectDevice{links: 4, linkCount: 18}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkState, gpusignals.NVLinkEndpointCapabilityValid)
	require.Equal(t, []int{0, 1, 2, 3, 4}, dev.states)
	last := batch.Observations[len(batch.Observations)-2:]
	for _, current := range last {
		require.Equal(t, uint32(4), current.Target.GetNvLinkEndpoint().GetLinkIndex())
		require.Equal(t, "ERROR_NOT_SUPPORTED", current.GetCollectionError().GetSourceError())
	}
}

func TestInterconnectLostMidLoopFailsUnreadLinks(t *testing.T) {
	dev := &interconnectDevice{links: 4, linkCount: 4, capReturns: map[nvml.NvLinkCapability]nvml.Return{nvml.NVLINK_CAP_VALID: nvml.ERROR_GPU_IS_LOST}}
	batch := interconnectBatch(context.Background(), dev, gpusignals.NVLinkState, gpusignals.NVLinkEndpointCapabilityValid)
	require.Equal(t, []int{0}, dev.states)
	gpuLevel := slices.DeleteFunc(batch.Observations, func(o *observation.Observation) bool { return o.Target.GetGpu() == nil })
	require.Len(t, gpuLevel, 2)
	for _, current := range gpuLevel {
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, current.GetCollectionError().GetCategory())
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
