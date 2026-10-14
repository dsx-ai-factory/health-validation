package nvlink

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NVLinkFabric returns the check definition for NVLink fabric
// registration.
//
// On GB200/GB300 a GPU must register with the shared NVLink fabric before it
// can route traffic to its peers; a GPU that never completes
// registration, or completes it with a failure status, is cut off from
// multi-GPU NVLink collectives. Both conditions are collected per GPU
// from the same fabric-info query that feeds [NVLinkFabricHealth].
//
// The check models one severity tier:
//   - CRITICAL: fabric registration has not completed, or completed with
//     a failure status.
//   - PASS: registration completed successfully.
func NVLinkFabric() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_fabric",
		Name:        "NVLink Fabric Registration",
		Description: "Verifies each GPU has completed NVLink fabric registration with a successful status. Incomplete or failed registration cuts the GPU off from multi-GPU NVLink communication.",
		Category:    health.NVLink,

		// Reads the same NVML per-device enumeration that gpu_count
		// measures, so a verdict about a device missing from that
		// enumeration names gpu_count as the cause.
		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalFabricRegistrationIncomplete,
			SignalFabricRegistrationFailed,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		ConditionScope:    health.DomainScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Rules: []health.Rule{
			{
				Expression: "fabric_registration_incomplete == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric registration has not completed.",
			},
			{
				Expression: "fabric_registration_failed == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric registration completed with a failure status.",
			},
		},
	}
}
