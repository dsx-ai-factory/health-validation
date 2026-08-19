package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPowerLimit returns the check definition that verifies each GPU's
// current and enforced power limits.
//
// Both the threshold and the observed signals (power_*_mw) are milliwatts,
// so CEL compares them directly with no unit conversion — this also keeps
// readings exact, since a mW→W rounding could hide an anomalous
// fractional-watt limit. Limits must match expected_power_limit_mw, which
// defaults per architecture (operators may override). The catalog Value is
// a placeholder (GB200's 1200 W) used only when architecture filtering is
// off; declared arches resolve via PerArchitecture.
func GPUPowerLimit() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_power_limit",
		Name:        "GPU Power Limit",
		Description: "Verifies each GPU's current and enforced power limits.",
		Category:    health.GPU,

		RequiredSignals: []string{
			SignalGPUID,
			SignalPowerLimitMW,
			SignalEnforcedPowerLimitMW,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "H100"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.DiagnosticActionCheckMechanicals),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_power_limit_mw": {
				// Placeholder when architecture filtering is off. Declared
				// arches resolve via PerArchitecture (GB200 1200 W).
				Value: int64(1_200_000),
				PerArchitecture: map[string]any{
					"GB200": int64(1_200_000),
					"GB300": int64(1_400_000),
					"H100":  int64(700_000),
				},
				Category:    health.DefaultThreshold,
				Description: "Current and enforced power limit expected for every GPU.",
				Unit:        "mW",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "power_limit_mw != expected_power_limit_mw",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: current power limit ${power_limit_mw} mW, expected ${expected_power_limit_mw} mW.",
			},
			{
				Expression: "enforced_power_limit_mw != expected_power_limit_mw",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: enforced power limit ${enforced_power_limit_mw} mW, expected ${expected_power_limit_mw} mW.",
			},
		},
	}
}
