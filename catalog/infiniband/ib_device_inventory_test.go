package infiniband_test

import (
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/infiniband"
)

func inventoryCatalog(expected ...string) *catalog.Catalog {
	return catalog.New(
		catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
			CheckID:       "ib_device_inventory",
			ThresholdName: "expected_devices",
			Value:         expected,
		})),
	)
}

func TestDeviceInventoryExactMatch(t *testing.T) {
	v, err := inventoryCatalog("mlx5_0", "mlx5_1").Evaluate(
		"ib_device_inventory",
		infiniband.InventoryMeasurement{Devices: []string{"mlx5_1", "mlx5_0"}},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS", v.Severity)
	}
}

func TestDeviceInventoryMissingDevice(t *testing.T) {
	v, err := inventoryCatalog("mlx5_0", "mlx5_1").Evaluate(
		"ib_device_inventory",
		infiniband.InventoryMeasurement{Devices: []string{"mlx5_0"}},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestDeviceInventoryUnexpectedDevice(t *testing.T) {
	v, err := inventoryCatalog("mlx5_0").Evaluate(
		"ib_device_inventory",
		infiniband.InventoryMeasurement{Devices: []string{"mlx5_0", "mlx5_1"}},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

// An unconfigured fleet has no expected inventory to compare against, so the
// check skips rather than draining every node over a configuration gap.
func TestDeviceInventorySkipsWhenUnconfigured(t *testing.T) {
	v, err := catalog.New(catalog.WithExecutionContext(health.OperatorInfra)).Evaluate(
		"ib_device_inventory",
		infiniband.InventoryMeasurement{Devices: []string{"mlx5_0"}},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ThresholdUnconfigured {
		t.Errorf("skipped = %v, reason = %v, want skipped with THRESHOLD_UNCONFIGURED", v.Skipped, v.SkipReason)
	}
	if !strings.Contains(v.SkipDetail, "expected_devices") {
		t.Errorf("skip detail = %q, want the unconfigured threshold named", v.SkipDetail)
	}
}

// An explicitly empty inventory is configured, not unset: the check runs and
// fails a node that enumerated devices it was not supposed to have.
func TestDeviceInventoryEmptyExpectationIsConfigured(t *testing.T) {
	v, err := inventoryCatalog().Evaluate(
		"ib_device_inventory",
		infiniband.InventoryMeasurement{Devices: []string{"mlx5_0"}},
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Skipped {
		t.Fatalf("check skipped (%v); an explicit empty list is a configured expectation", v.SkipReason)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}
