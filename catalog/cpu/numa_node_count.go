package cpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NUMANodeCount returns the check definition that verifies a node brought online
// the expected number of NUMA nodes for its architecture.
//
// The check fires Critical when the online NUMA node count differs from the
// expected value — typically a platform firmware setting, a kernel boot
// argument, or a node provisioned as the wrong SKU. A reboot is required for a
// corrected firmware or boot-argument setting to take effect.
//
// Covers GB200 and GB300 (34 nodes). A100, H100, and H200 are not yet covered;
// they need their own PerArchitecture entries before being added.
// On these platforms the count spans more than the CPU sockets: accelerator
// memory is exposed as additional nodes, so the expected value tracks the
// whole platform topology rather than the socket count alone.
func NUMANodeCount() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "numa_node_count",
		Name:        "NUMA Node Count",
		Description: "Verifies a node brought online the expected number of NUMA nodes for its architecture.",
		Category:    health.CPU,

		RequiredSignals: []string{SignalNUMANodeCount},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.RemediationActionReboot),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_numa_node_count": {
				Value: int64(34),
				PerArchitecture: map[string]any{
					"GB200": int64(34),
					"GB300": int64(34),
				},
				Category:    health.DefaultThreshold,
				Description: "Expected number of NUMA nodes online. GB200 and GB300 at 34. Override per fleet.",
				Unit:        "count",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "numa_node_count != expected_numa_node_count",
				Severity:   health.Critical,
				Message:    "Expected ${expected_numa_node_count} NUMA nodes online, found ${numa_node_count}.",
			},
		},
	}
}
