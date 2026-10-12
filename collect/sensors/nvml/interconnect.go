// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func interconnectSignals() collect.Signals { return gpusignals.Interconnect() }

var (
	linkSignals   = gpusignals.EndpointSignals()
	fabricSignals = collect.Signals{gpusignals.NVLinkFabricState, gpusignals.NVLinkFabricStatus, gpusignals.NVLinkFabricCliqueID, gpusignals.NVLinkFabricClusterUUID, gpusignals.NVLinkFabricHealthMask, gpusignals.NVLinkFabricHealthSummary, gpusignals.NVLinkFabricBandwidthDegraded, gpusignals.NVLinkFabricRouteRecoveryActive, gpusignals.NVLinkFabricRouteUnhealthy, gpusignals.NVLinkFabricAccessTimeoutRecovery}
)

func measureInterconnect(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) {
	r := rawReads{ctx: ctx, batch: batch, now: now, target: target, want: want}
	if r.selected(linkSignals) {
		if !r.canRead() {
			for _, definition := range linkSignals {
				r.record(definition, "GetNvLinkState", nvml.SUCCESS, nil)
			}
		} else {
			counters := []nvml.NvLinkErrorCounter{nvml.NVLINK_ERROR_DL_REPLAY, nvml.NVLINK_ERROR_DL_RECOVERY, nvml.NVLINK_ERROR_DL_CRC_FLIT}
			for link := range int(nvml.NVLINK_MAX_LINKS) {
				state, ret := nvml.FEATURE_DISABLED, nvml.SUCCESS
				if r.canRead() {
					state, ret = dev.GetNvLinkState(link)
				}
				if ret == nvml.ERROR_FUNCTION_NOT_FOUND {
					r.target = target
					for _, definition := range linkSignals {
						r.record(definition, "GetNvLinkState", ret, nil)
					}
					break
				}
				// A higher unsupported index terminates the contiguous link shape.
				// Other failed state reads retain their endpoint and do not hide later links.
				if ret == nvml.ERROR_NOT_SUPPORTED {
					if link == 0 {
						r.target = target
						for _, definition := range linkSignals {
							r.record(definition, "GetNvLinkState", nvml.ERROR_NOT_SUPPORTED, nil)
						}
					}
					break
				}
				r.target = observation.NVLinkEndpointTarget(target.GetGpu().GetUuid(), uint32(link))
				r.record(gpusignals.NVLinkState, "GetNvLinkState", ret, rawUint(uint64(state)))
				r.note(ret)
				if ret != nvml.SUCCESS {
					for _, definition := range linkSignals[1:] {
						r.record(definition, "GetNvLinkState", ret, nil)
					}
					if !r.canRead() {
						break
					}
					continue
				}
				for i, counter := range counters {
					definition := linkSignals[i+1]
					if !want[definition.ID()] {
						continue
					}
					value, counterRet := uint64(0), nvml.SUCCESS
					if r.canRead() {
						value, counterRet = dev.GetNvLinkErrorCounter(link, counter)
					}
					r.record(definition, "GetNvLinkErrorCounter", counterRet, rawUint(value))
				}
				if !r.canRead() {
					break
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

func clusterUUID(raw [16]uint8) string {
	if raw == ([16]uint8{}) {
		return ""
	}
	var encoded [32]byte
	hex.Encode(encoded[:], raw[:])
	return string(encoded[:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:])
}
