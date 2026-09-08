// Package nvlink defines health check definitions for NVLink hardware.
package nvlink

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NVLinkFabricHealth returns the check definition for NVLink fabric
// health monitoring.
//
// This check evaluates the per GPU NVLink fabric health flags reported
// by NVML. On GB200, GPUs communicate through a shared NVLink fabric;
// when a GPU reports its fabric connection as degraded or recovering,
// multi-GPU collectives slow down or stall across the NVLink domain.
// The flags decode from a single GPU fabric info health bitmask, so one
// NVML query per GPU covers every condition below.
//
// The check models one severity tier:
//   - CRITICAL: fabric bandwidth is not Full, fabric route recovery is
//     in progress, a fabric route is unhealthy, or fabric access
//     timeout recovery is active. Any of these indicates a degraded or
//     recovering NVLink fabric affecting multi-GPU communication.
//   - PASS: all fabric health flags are nominal.
func NVLinkFabricHealth() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_fabric_health",
		Name:        "NVLink Fabric Health",
		Description: "Monitors per-GPU NVLink fabric health flags from NVML. Degraded bandwidth, in-progress route recovery, an unhealthy route, or active access timeout recovery indicate a fabric problem affecting multi-GPU communication.",
		Category:    health.NVLink,

		RequiredSignals: []string{
			SignalGPUID,
			SignalFabricBandwidthDegraded,
			SignalFabricRouteRecoveryInProgress,
			SignalFabricRouteUnhealthy,
			SignalFabricAccessTimeoutRecovery,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		ConditionScope: health.DomainScope,
		Provenance:     health.NVIDIA,
		MaturityLevel:  health.Stable,
		// Unlike the generic ladder, the last step is hold rather than
		// contact_support: an NVLink fabric/route fault is often a
		// shared-switch problem, not a single-node one, so escalation
		// should wait for enough held nodes in the domain to confirm
		// that before involving support — see RemediationActionHold.
		RecommendedAction: health.Drain(health.RemediationActionResetGPU, health.RemediationActionRestartVM, health.RemediationActionHold),

		Rules: []health.Rule{
			{
				Expression: "fabric_bandwidth_degraded == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric bandwidth is not Full.",
			},
			{
				Expression: "fabric_route_recovery_in_progress == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric route recovery is in progress.",
			},
			{
				Expression: "fabric_route_unhealthy == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric has an unhealthy route.",
			},
			{
				Expression: "fabric_access_timeout_recovery == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric access timeout recovery is active.",
			},
		},
	}
}
