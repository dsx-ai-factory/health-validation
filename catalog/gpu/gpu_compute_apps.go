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
// runs in the tenant guest, and under OPERATOR_INFRA for operators who
// run it on the host before and after their own jobs, such as perf
// tests. NVML lists only the processes in the caller's PID namespace, and
// a host cannot see the processes of a tenant VM it hands the GPUs to, so
// where the jobs run in a VM the check must run in that guest: on the
// host, an empty list would pass while a job still held the GPU. A
// process still holds the GPU, so a GPU reset cannot run; the
// recommendation is to restart the node, then escalate.
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
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "B200", "B300", "H100", "A100", "VR"},

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
