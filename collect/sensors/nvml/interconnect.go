// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func interconnectSignals() collect.Signals { return gpusignals.Interconnect() }

var (
	gpuLinkSignals = collect.Signals{gpusignals.NVLinkLinkCount, gpusignals.NVLinkSpeedCommon}
	linkSignals    = gpusignals.EndpointSignals()
	// loopSignals are the endpoint signals read per link after its state. Link
	// speed is read for all links at once, whatever their state, as legacy does.
	loopSignals = slices.DeleteFunc(slices.Clone(linkSignals), func(s collect.Signal) bool {
		return s.ID() == gpusignals.NVLinkEndpointSpeed.ID()
	})
	linkCounters = []struct {
		definition collect.Signal
		counter    nvml.NvLinkErrorCounter
	}{
		{gpusignals.NVLinkEndpointReplayErrorCount, nvml.NVLINK_ERROR_DL_REPLAY},
		{gpusignals.NVLinkEndpointRecoveryErrorCount, nvml.NVLINK_ERROR_DL_RECOVERY},
		{gpusignals.NVLinkEndpointCRCFlitErrorCount, nvml.NVLINK_ERROR_DL_CRC_FLIT},
	}
	linkCapabilities = []struct {
		definition collect.Signal
		capability nvml.NvLinkCapability
	}{
		{gpusignals.NVLinkEndpointCapabilityP2PSupported, nvml.NVLINK_CAP_P2P_SUPPORTED},
		{gpusignals.NVLinkEndpointCapabilitySysmemAccess, nvml.NVLINK_CAP_SYSMEM_ACCESS},
		{gpusignals.NVLinkEndpointCapabilityP2PAtomics, nvml.NVLINK_CAP_P2P_ATOMICS},
		{gpusignals.NVLinkEndpointCapabilitySysmemAtomics, nvml.NVLINK_CAP_SYSMEM_ATOMICS},
		{gpusignals.NVLinkEndpointCapabilitySLIBridge, nvml.NVLINK_CAP_SLI_BRIDGE},
		{gpusignals.NVLinkEndpointCapabilityValid, nvml.NVLINK_CAP_VALID},
	}
	fabricSignals = collect.Signals{gpusignals.NVLinkFabricState, gpusignals.NVLinkFabricStatus, gpusignals.NVLinkFabricCliqueID, gpusignals.NVLinkFabricClusterUUID, gpusignals.NVLinkFabricHealthMask, gpusignals.NVLinkFabricHealthSummary, gpusignals.NVLinkFabricBandwidthDegraded, gpusignals.NVLinkFabricRouteRecoveryActive, gpusignals.NVLinkFabricRouteUnhealthy, gpusignals.NVLinkFabricAccessTimeoutRecovery}
)

func measureInterconnect(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) {
	r := rawReads{ctx: ctx, batch: batch, now: now, target: target, want: want}
	links, bounded := int(nvml.NVLINK_MAX_LINKS), false
	if r.selected(gpuLinkSignals) || r.selected(linkSignals) {
		values := []nvml.FieldValue{{FieldId: nvml.FI_DEV_NVLINK_LINK_COUNT}, {FieldId: nvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON}}
		ret := nvml.SUCCESS
		if r.canRead() {
			ret = dev.GetFieldValues(values)
			r.note(ret)
		}
		count, countOK := r.recordField(gpusignals.NVLinkLinkCount, ret, values[0])
		r.recordField(gpusignals.NVLinkSpeedCommon, ret, values[1])
		// Past-the-end link returns differ by platform, so a known count bounds
		// the loop. A count of 0 does not, so a GPU without NVLink still reports
		// NOT_SUPPORTED at link 0.
		if countOK && count > 0 && count <= uint64(links) {
			links, bounded = int(count), true
		}
		if want[gpusignals.NVLinkEndpointSpeed.ID()] {
			countRet := ret
			if countRet == nvml.SUCCESS {
				countRet = nvml.Return(values[0].NvmlReturn)
			}
			measureLinkSpeeds(&r, dev, links, bounded, countRet)
		}
	}
	if r.selected(loopSignals) {
		if !r.canRead() {
			for _, definition := range loopSignals {
				r.record(definition, "GetNvLinkState", nvml.SUCCESS, nil)
			}
		} else {
			for link := range links {
				state, ret := nvml.FEATURE_DISABLED, nvml.SUCCESS
				if r.canRead() {
					state, ret = dev.GetNvLinkState(link)
				}
				// FUNCTION_NOT_FOUND, or NOT_SUPPORTED at link 0, means the GPU
				// reports no links at all.
				if ret == nvml.ERROR_FUNCTION_NOT_FOUND || (ret == nvml.ERROR_NOT_SUPPORTED && link == 0) {
					r.target = target
					for _, definition := range loopSignals {
						r.record(definition, "GetNvLinkState", ret, nil)
					}
					break
				}
				r.target = observation.NVLinkEndpointTarget(target.GetGpu().GetUuid(), uint32(link))
				r.record(gpusignals.NVLinkState, "GetNvLinkState", ret, rawUint(uint64(state)))
				r.note(ret)
				if ret != nvml.SUCCESS {
					for _, definition := range loopSignals {
						if definition.ID() != gpusignals.NVLinkState.ID() {
							r.record(definition, "GetNvLinkState", ret, nil)
						}
					}
					// A higher unsupported index ends the links. It is still
					// recorded, so capabilities cannot pass on the links before it.
					if ret == nvml.ERROR_NOT_SUPPORTED || !r.canRead() {
						break
					}
					continue
				}
				for _, c := range linkCounters {
					if !want[c.definition.ID()] {
						continue
					}
					value, counterRet := uint64(0), nvml.SUCCESS
					if r.canRead() {
						value, counterRet = dev.GetNvLinkErrorCounter(link, c.counter)
					}
					r.record(c.definition, "GetNvLinkErrorCounter", counterRet, rawUint(value))
				}
				for _, c := range linkCapabilities {
					if !want[c.definition.ID()] {
						continue
					}
					value, capabilityRet := uint32(0), nvml.SUCCESS
					if r.canRead() {
						value, capabilityRet = dev.GetNvLinkCapability(link, c.capability)
					}
					r.record(c.definition, "GetNvLinkCapability", capabilityRet, rawUint(uint64(value)))
				}
				if !r.canRead() {
					break
				}
			}
			// A GPU lost mid-loop leaves later links unread; fail them at the
			// GPU so no link aggregate passes on the links read so far.
			if !r.canRead() {
				r.target = target
				for _, definition := range loopSignals {
					r.record(definition, "GetNvLinkState", nvml.SUCCESS, nil)
				}
			}
		}
	}
	r.target = target
	if !r.selected(fabricSignals) {
		return
	}
	info, baseRet, healthRet := nvml.GpuFabricInfo_v3{}, nvml.SUCCESS, nvml.SUCCESS
	if r.canRead() {
		info, baseRet, healthRet = dev.GetFabricInfo()
	}
	r.note(baseRet)
	r.note(healthRet)
	baseValues := []*observation.Value{rawUint(uint64(info.State)), rawUint(uint64(info.Status)), rawUint(uint64(info.CliqueId)), {Kind: &observation.Value_StringValue{StringValue: clusterUUID(info.ClusterUuid)}}}
	for i, definition := range fabricSignals[:4] {
		r.record(definition, "GetGpuFabricInfo", baseRet, baseValues[i])
	}
	r.record(gpusignals.NVLinkFabricHealthMask, "GetGpuFabricInfoV", healthRet, rawUint(uint64(info.HealthMask)))
	r.record(gpusignals.NVLinkFabricHealthSummary, "GetGpuFabricInfoV", healthRet, rawUint(uint64(info.HealthSummary)))
	healthBits := []struct {
		shift uint32
		width uint32
	}{
		{nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW},
		{nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY},
		{nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY},
		{nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY, nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY},
	}
	for i, bit := range healthBits {
		definition := fabricSignals[i+6]
		if !want[definition.ID()] {
			continue
		}
		// Unsupported fabric health reads as NoValue, as the DCGM source reports it.
		if healthRet == nvml.ERROR_NOT_SUPPORTED && r.canRead() {
			r.noValue(definition, "ERROR_NOT_SUPPORTED", "The driver does not support fabric health.")
			continue
		}
		if healthRet != nvml.SUCCESS || !r.canRead() {
			r.record(definition, "GetGpuFabricInfoV", healthRet, nil)
			continue
		}
		value := (info.HealthMask >> bit.shift) & bit.width
		switch value {
		case 0:
			r.noValue(definition, "NVML_FABRIC_HEALTH_NOT_SUPPORTED", "The driver does not support this fabric health field.")
		case 1, 2:
			r.record(definition, "GetGpuFabricInfoV", nvml.SUCCESS, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: value == 1}})
		default:
			r.batch.Observations = append(r.batch.Observations, errorObservation(now, target, definition, "GetGpuFabricInfoV", observation.CollectionErrorCategoryParseError, fmt.Sprintf("FABRIC_HEALTH_%d", value), "Unknown fabric health mask encoding."))
		}
	}
}

// measureLinkSpeeds reads every link's speed in one call. Speeds are read
// only below a known link count; otherwise the signal fails at the GPU with
// the count's return.
func measureLinkSpeeds(r *rawReads, dev device.Device, links int, bounded bool, countRet nvml.Return) {
	if !bounded {
		if countRet == nvml.SUCCESS {
			countRet = nvml.ERROR_NOT_SUPPORTED
		}
		r.record(gpusignals.NVLinkEndpointSpeed, "GetFieldValues", countRet, nil)
		return
	}
	values := make([]nvml.FieldValue, links)
	for link := range values {
		values[link] = nvml.FieldValue{FieldId: nvml.FI_DEV_NVLINK_GET_SPEED, ScopeId: uint32(link)}
	}
	ret := nvml.SUCCESS
	if r.canRead() {
		ret = dev.GetFieldValues(values)
	}
	gpu := r.target
	for link := range values {
		r.target = observation.NVLinkEndpointTarget(gpu.GetGpu().GetUuid(), uint32(link))
		r.recordField(gpusignals.NVLinkEndpointSpeed, ret, values[link])
	}
	r.target = gpu
}

// measurePeers reads NVLink P2P status for every ordered pair of distinct
// GPUs. A lost GPU is not a source: its selected signals already failed.
func measurePeers(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, devices map[string]device.Device, lost, want map[string]bool) {
	if !want[gpusignals.NVLinkP2PStatus.ID()] {
		return
	}
	uuids := slices.Sorted(maps.Keys(devices))
	for _, gpu := range uuids {
		if lost[gpu] {
			continue
		}
		if len(uuids) == 1 {
			// Say a lone GPU has no peers, so its count is not read as uncollected.
			definition := gpusignals.NVLinkP2PStatus
			batch.Observations = append(batch.Observations, observation.NewNoValueObservation(definition.ID(), definition.Unit(), source, observation.GPUTarget(gpu), now,
				&observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "No peer GPUs"}))
			continue
		}
		for _, peer := range uuids {
			if peer == gpu {
				continue
			}
			// One reader per pair: a lost-GPU return may name the peer, so it
			// must not stop this source's other pairs.
			r := rawReads{ctx: ctx, batch: batch, now: now, target: observation.GPUPairTarget(gpu, peer), want: want}
			status, ret := nvml.P2P_STATUS_OK, nvml.SUCCESS
			if r.canRead() {
				status, ret = devices[gpu].GetP2PStatus(devices[peer], nvml.P2P_CAPS_INDEX_NVLINK)
			}
			r.record(gpusignals.NVLinkP2PStatus, "GetP2PStatus", ret, rawUint(uint64(status)))
		}
	}
}

// recordField records one unsigned-int field value from GetFieldValues and
// returns it when the read succeeded, whether or not the signal is wanted.
func (r *rawReads) recordField(definition collect.Signal, callRet nvml.Return, value nvml.FieldValue) (uint64, bool) {
	ret := callRet
	if ret == nvml.SUCCESS {
		ret = nvml.Return(value.NvmlReturn)
	}
	if ret != nvml.SUCCESS || !r.canRead() {
		r.record(definition, "GetFieldValues", ret, nil)
		return 0, false
	}
	if nvml.ValueType(value.ValueType) != nvml.VALUE_TYPE_UNSIGNED_INT {
		if r.want[definition.ID()] {
			r.batch.Observations = append(r.batch.Observations, errorObservation(r.now, r.target, definition, "GetFieldValues", observation.CollectionErrorCategoryParseError, fmt.Sprintf("VALUE_TYPE_%d", value.ValueType), "Unexpected NVML field value type."))
		}
		return 0, false
	}
	decoded := uint64(binary.LittleEndian.Uint32(value.Value[:4]))
	r.record(definition, "GetFieldValues", nvml.SUCCESS, rawUint(decoded))
	return decoded, true
}

func clusterUUID(raw [16]uint8) string {
	if raw == ([16]uint8{}) {
		return ""
	}
	var encoded [32]byte
	hex.Encode(encoded[:], raw[:])
	return string(encoded[:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:])
}
