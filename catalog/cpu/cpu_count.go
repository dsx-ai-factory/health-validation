package cpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// CPUCount returns the check definition that verifies every logical CPU in the
// kernel's possible mask is online.
func CPUCount() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "cpu_count",
		Name:        "System CPU Count",
		Description: "Verifies every logical CPU in the Linux kernel's possible mask is online and available for scheduling.",
		Category:    health.CPU,

		RequiredSignals: []string{
			SignalPossibleCPUCount,
			SignalOnlineCPUCount,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Rules: []health.Rule{
			{
				Expression: "online_cpu_count != possible_cpu_count",
				Severity:   health.Critical,
				Message:    "Expected all ${possible_cpu_count} possible logical CPUs to be online; found ${online_cpu_count} online.",
			},
		},
	}
}
