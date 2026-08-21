package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPowerLimit returns the check definition that verifies each GPU's
// current and enforced power limits.
//
// The threshold and observed signals are watts, including fractional values
// converted from NVML's milliwatt readings. Limits must match
// expected_power_limit_w, which defaults per architecture (operators may
// override). The catalog Value is a placeholder (GB200's 1200 W) used only
// when architecture filtering is off; declared arches resolve via
// PerArchitecture.
func GPUPowerLimit() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_power_limit",
		Name:        "GPU Power Limit",
		Description: "Verifies each GPU's current and enforced power limits.",
		Category:    health.GPU,

		RequiredSignals: []string{
			SignalGPUID,
			SignalPowerLimitW,
			SignalEnforcedPowerLimitW,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "H100"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.DiagnosticActionCheckMechanicals),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_power_limit_w": {
				// Placeholder when architecture filtering is off. Declared
				// arches resolve via PerArchitecture (GB200 1200 W).
				Value: float64(1200),
				PerArchitecture: map[string]any{
					"GB200": float64(1200),
					"GB300": float64(1400),
					"H100":  float64(700),
				},
				Category:    health.DefaultThreshold,
				Description: "Current and enforced power limit expected for every GPU.",
				Unit:        "W",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "power_limit_w != expected_power_limit_w",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: current power limit ${power_limit_w} W, expected ${expected_power_limit_w} W.",
			},
			{
				Expression: "enforced_power_limit_w != expected_power_limit_w",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: enforced power limit ${enforced_power_limit_w} W, expected ${expected_power_limit_w} W.",
			},
		},
	}
}
