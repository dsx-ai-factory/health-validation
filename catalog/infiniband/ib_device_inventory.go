package infiniband

import "github.com/NVIDIA/health-validation/catalog/health"

// IBDeviceInventory returns the check definition that verifies a node
// enumerates exactly the InfiniBand devices expected for its class.
//
// It fires Critical when an expected device is missing — the driver did not
// load, the card failed, or it came up in Ethernet mode — or when a device
// appears that the fleet was not configured for. Device names vary by
// platform, so expected_devices is operator-supplied; unconfigured, the
// check skips.
func IBDeviceInventory() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "ib_device_inventory",
		Name:        "InfiniBand Device Inventory",
		Description: "Verifies that all expected RDMA devices, and no unexpected devices, are enumerated.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{SignalDevices},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,

		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.DiagnosticActionRetestHardware),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_devices": {
				Value:            []string{},
				Category:         health.DefaultThreshold,
				RequiredOverride: true,
				Description:      "Exact RDMA device names expected on this class of node. Must be configured per fleet.",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "expected_devices.exists(expected, !(expected in devices)) || " +
					"devices.exists(actual, !(actual in expected_devices))",
				Severity: health.Critical,
				Message:  "Expected InfiniBand devices ${expected_devices}, discovered ${devices}.",
			},
		},
	}
}
