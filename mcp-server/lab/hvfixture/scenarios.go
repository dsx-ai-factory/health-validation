// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/agentkit/verdictserver"
	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// A scenario is a node state made of synthetic measurements. Catalog scenarios are
// judged by hv's real catalog; discovery-failure and busy-source are built the way
// toolkit/app builds them, because no measurement produces those verdicts.
type scenario struct {
	evaluate func(cadence *health.SchedulingCadence) (health.NodeVerdict, error)
	liveness verdictserver.LivenessFunc
	claim    string
}

// The tenant-set process name in the Xid event. The MCP server must never repeat it.
const tenantText = "python train.py --token=FIXTURE-SECRET"

var gpuIDs = []string{
	"GPU-00000000-0000-4000-8000-000000000000",
	"GPU-11111111-1111-4111-8111-111111111111",
	"GPU-22222222-2222-4222-8222-222222222222",
	"GPU-33333333-3333-4333-8333-333333333333",
}

// stuckSince is when the busy-source scenario's nvml call started.
var stuckSince = time.Now().Add(-90 * time.Second)

func healthy() verdictserver.Liveness { return verdictserver.Liveness{OK: true} }

func stuck() verdictserver.Liveness {
	return verdictserver.Liveness{StuckCalls: []verdictserver.StuckCall{{Source: "nvml", Since: stuckSince}}}
}

var scenarios = map[string]scenario{
	"healthy-gb200": {
		claim: "PASS: a GB200 node with 4 GPUs whose GPU, NVLink and Xid checks all run and pass",
		evaluate: func(cadence *health.SchedulingCadence) (health.NodeVerdict, error) {
			return judge("GB200", nil, cadence, gb200(nil)...)
		},
		liveness: healthy,
	},
	"nvlink-down": {
		claim: "CRITICAL nvlink_active: every NVLink on one GPU is inactive",
		evaluate: func(cadence *health.SchedulingCadence) (health.NodeVerdict, error) {
			return judge("GB200", nil, cadence, gb200(func(_ *gpu.GPUMeasurement, links *nvlink.NVLinkMeasurement) {
				if links.GPUID == gpuIDs[2] {
					links.InactiveCount = links.LinkCount
				}
			})...)
		},
		liveness: healthy,
	},
	"xid79": {
		claim: "CRITICAL gpu_xid_79: one GPU fell off the bus, with tenant text in the journal event",
		evaluate: func(cadence *health.SchedulingCadence) (health.NodeVerdict, error) {
			measurements := append(gb200(nil), &xid.XidMeasurement{
				GPUID: gpuIDs[1], PCIAddress: "0000:09:00", Since: "0", EventCount: 1,
				XidCounts: map[int64]int64{79: 1}, FatalXidCounts: map[int64]int64{79: 1},
				Events: []xid.XidEvent{{
					XidCode: 79, Pid: 4242, ProcessName: tenantText,
					Message: "NVRM: Xid (PCI:0000:09:00): 79, pid=4242, name=" + tenantText + ", GPU has fallen off the bus.",
				}},
			})

			return judge("GB200", nil, cadence, measurements...)
		},
		liveness: healthy,
	},
	"busy-source": {
		claim: "UNKNOWN collection_source_busy: an earlier nvml call has not returned; /healthz says unhealthy",
		evaluate: func(*health.SchedulingCadence) (health.NodeVerdict, error) {
			since := stuckSince.Format(time.RFC3339Nano)

			return health.AggregateNodeVerdict([]health.CheckVerdict{{
				Timestamp: time.Now(), CheckID: "collection_source_busy", Category: health.Node, Severity: health.Unknown,
				RecommendedAction: health.Drain(health.RemediationActionRetryCollection),
				Messages:          []string{fmt.Sprintf("source %q has an unfinished call started at %s", "nvml", since)},
				ObservedValues:    map[string]any{"source": "nvml", "since": since},
			}}), nil
		},
		liveness: stuck,
	},
	"discovery-failure": {
		claim: "CRITICAL architecture_discovery: the GPUs could not be enumerated",
		evaluate: func(*health.SchedulingCadence) (health.NodeVerdict, error) {
			return health.AggregateNodeVerdict([]health.CheckVerdict{{
				Timestamp: time.Now(), CheckID: "architecture_discovery", Category: health.Node,
				ConditionScope: health.NodeScope, Severity: health.Critical,
				Messages: []string{fmt.Sprintf("could not determine GPU architecture: %v",
					errors.New("synthetic: no GPU inventory"))},
				RecommendedAction: health.GenericDrain(), MaturityLevel: health.Stable, NVIDIAEndorsed: true,
			}}), nil
		},
		liveness: healthy,
	},
	"unknown-architecture": {
		claim: "PASS where only cpu_count runs: the GPU is not an architecture the catalog knows",
		evaluate: func(cadence *health.SchedulingCadence) (health.NodeVerdict, error) {
			return judge("unknown", nil, cadence, healthyCPU(), healthyGPU(gpuIDs[0]),
				&xid.XidMeasurement{XidCounts: map[int64]int64{}, FatalXidCounts: map[int64]int64{}, Since: "0"})
		},
		liveness: healthy,
	},
	"overridden-threshold": {
		claim: "CRITICAL gpu_average_power_draw: the site caps max_power_w at 500 W and a GPU draws 600 W, " +
			"which the catalog default of 1500 W passes",
		evaluate: func(cadence *health.SchedulingCadence) (health.NodeVerdict, error) {
			return judge("GB200", powerCap(), cadence, gb200(nil)...)
		},
		liveness: healthy,
	},
}

func scenarioNames() []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func powerCap() []catalog.Option {
	return []catalog.Option{catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID: "gpu_average_power_draw", ThresholdName: "max_power_w", Value: 500,
	}))}
}

func judge(architecture string, options []catalog.Option, cadence *health.SchedulingCadence,
	measurements ...health.Measurement,
) (health.NodeVerdict, error) {
	built := catalog.New(append([]catalog.Option{catalog.WithArchitecture(architecture)}, options...)...)

	return built.EvaluateAllWithOptions(catalog.EvaluationOptions{Cadence: cadence}, measurements...)
}

func healthyCPU() cpu.CPUMeasurement {
	return cpu.CPUMeasurement{PossibleCPUCount: 144, OnlineCPUCount: 144, NUMANodeCount: 34}
}

func healthyGPU(id string) *gpu.GPUMeasurement {
	return &gpu.GPUMeasurement{GPUID: id, AveragePowerDrawW: 600, PowerLimitW: 1200, EnforcedPowerLimitW: 1200}
}

func healthyLinks(id string) *nvlink.NVLinkMeasurement {
	return &nvlink.NVLinkMeasurement{
		GPUID: id, LinkCount: 18, SpeedMBytesPerSec: 53125,
		AllLinksSupportP2P: true, AllLinksSupportSysmemAccess: true, AllLinksSupportP2PAtomics: true,
		AllLinksSupportSysmemAtomics: true, AllLinksSupportSLI: true, AllLinksSupportLink: true,
	}
}

// gb200 is a healthy GB200 node; change, when set, edits each GPU's measurements.
func gb200(change func(*gpu.GPUMeasurement, *nvlink.NVLinkMeasurement)) []health.Measurement {
	measurements := []health.Measurement{
		healthyCPU(),
		node.NodeMeasurement{GPUCount: int64(len(gpuIDs))},
		&xid.XidMeasurement{XidCounts: map[int64]int64{}, FatalXidCounts: map[int64]int64{}, Since: "0"},
	}

	for _, id := range gpuIDs {
		device, links := healthyGPU(id), healthyLinks(id)
		if change != nil {
			change(device, links)
		}

		measurements = append(measurements, device, links)
	}

	return measurements
}
