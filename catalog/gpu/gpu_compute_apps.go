package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUComputeApps returns the check definition that fails when any compute
// context is still holding a GPU.
//
// The check fires Critical when compute_process_count is greater than
// zero. Intended for exclusive-node pre/post-job windows: leftover CUDA
// processes, not graphics or scheduler-owned user-process cleanup.
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
		Architectures:     []string{"GB200", "GB300", "H100", "A100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.RemediationActionResetGPU),

		Rules: []health.Rule{
			{
				Expression: "compute_process_count > 0",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: ${compute_process_count} compute process(es) still holding the GPU:\n${compute_process_cmdlines}",
			},
		},
	}
}
