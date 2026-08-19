package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUAveragePowerDraw returns the check definition that verifies each GPU's
// one-second average power draw is within its expected range.
func GPUAveragePowerDraw() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_average_power_draw",
		Name:        "GPU Average Power Draw",
		Description: "Verifies each GPU's one-second average power draw is within the expected range.",
		Category:    health.GPU,

		RequiredSignals: []string{
			SignalGPUID,
			SignalAveragePowerDrawMW,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.DiagnosticActionCheckMechanicals),

		Thresholds: map[string]health.ThresholdSpec{
			"min_power_mw": {
				Value:       int64(65_000),
				Category:    health.DefaultThreshold,
				Description: "Minimum expected one-second average power draw per GPU. Override per fleet.",
				Unit:        "mW",
			},
			"max_power_mw": {
				Value:       int64(1_500_000),
				Category:    health.DefaultThreshold,
				Description: "Maximum expected one-second average power draw per GPU. Override per fleet.",
				Unit:        "mW",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "average_power_draw_mw < min_power_mw",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: average power draw ${average_power_draw_mw} mW is below minimum ${min_power_mw} mW.",
			},
			{
				Expression: "average_power_draw_mw > max_power_mw",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: average power draw ${average_power_draw_mw} mW is above maximum ${max_power_mw} mW.",
			},
		},
	}
}
