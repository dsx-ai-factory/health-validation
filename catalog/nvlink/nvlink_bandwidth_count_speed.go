// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NVLinkBandwidthCountSpeed returns the check definition that verifies
// each GPU reports the expected number of NVLinks at the expected
// per-link speed.
//
// The check fires Critical when a GPU's NVLink link count or per-link
// speed differs from the configured expected value — typically a
// hardware misconfiguration, partial NVLink training, or a degraded
// link that silently halves multi-GPU collective throughput without
// raising an error.
//
// Covers GB200, GB300, and H100 with per-architecture expected speeds
// (53125 / 53125 / 26562 MB/s), all at 18 links. A100 (12 links) and H200
// are not yet covered; they need their own PerArchitecture entries — and
// A100 a per-architecture link count — before being added.
//
// Speed values are MegaBytes per second, matching NVML's
// FI_DEV_NVLINK_SPEED_MBPS_COMMON field. NVML's "MBPS" suffix is a
// misnomer: the B is bytes, not bits, so the unit is MegaBytes per
// second. For reference, NVLink 5 reports 53125 (≈ 53 GB/s); NVLink 4
// reports 26562 (≈ 26.5 GB/s).
func NVLinkBandwidthCountSpeed() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_bandwidth_count_speed",
		Name:        "NVLink Bandwidth, Count, and Speed",
		Description: "Verifies each GPU's NVLink link count and per-link speed match expected values.",
		Category:    health.NVLink,

		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalLinkCount,
			SignalSpeedMBytesPerSec,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200", "H100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_link_count": {
				Value:       int64(18),
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Expected NVLinks per GPU. GB200, GB300 and B200 expose 18 NVLink 5 links; H100 exposes 18 NVLink 4 links.",
				Unit:        "count",
			},
			"expected_speed_mbytes_per_sec": {
				Value: int64(53125),
				PerArchitecture: map[string]any{
					"GB200": int64(53125),
					"GB300": int64(53125),
					"B200":  int64(53125),
					"H100":  int64(26562),
				},
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Expected per-link NVLink speed in MegaBytes per second. GB200, GB300 and B200 NVLink 5 at 53125, H100 NVLink 4 at 26562.",
				Unit:        "MB/s",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "link_count != expected_link_count",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: expected ${expected_link_count} NVLinks, found ${link_count}.",
			},
			{
				Expression: "speed_mbytes_per_sec != expected_speed_mbytes_per_sec",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: expected ${expected_speed_mbytes_per_sec} MB/s per NVLink, found ${speed_mbytes_per_sec} MB/s.",
			},
		},
	}
}
