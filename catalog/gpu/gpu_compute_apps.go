// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUComputeApps returns the check definition that fails when any compute
// context is still holding a GPU.
//
// The check fires Critical when compute_process_count is greater than
// zero. Intended for exclusive-node pre/post-job windows: leftover CUDA
// processes, not graphics or scheduler-owned user-process cleanup. It
// runs only in the guest: a host or control-plane VM cannot see the
// guest's compute processes, so an empty host list would pass while a
// job was still holding the GPU. A process still holds the GPU, so a
// GPU reset cannot run; the recommendation is to restart the node, then
// escalate.
func GPUComputeApps() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_compute_apps",
		Name:        "GPU Compute Apps",
		Description: "Fails when any compute process still holds a GPU.",
		Category:    health.GPU,

		RequiredSignals: []string{
			SignalGPUID,
			SignalComputeProcessCount,
			SignalComputeProcessCmdlines,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "B200", "H100", "A100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),

		Rules: []health.Rule{
			{
				Expression: "compute_process_count > 0",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: ${compute_process_count} compute process(es) still holding the GPU:\n${compute_process_cmdlines}",
			},
		},
	}
}
