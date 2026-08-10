package catalog_test

import (
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/cpu"
	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/infiniband"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
	"github.com/NVIDIA/health-validation/catalog/xid"
)

// The built-in measurement types must satisfy the signal-name uniqueness
// invariant that signal-subset routing relies on: within a category, no
// two measurement types share a signal name. A new measurement type or a
// renamed signal that violates it fails here rather than silently
// misrouting a check at evaluation time.
func TestBuiltinMeasurementsSignalUniqueness(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(
		cpu.CPUMeasurement{},
		gpu.GPUMeasurement{},
		node.NodeMeasurement{},
		nvlink.NVLinkMeasurement{},
		xid.XidMeasurement{},
		infiniband.InventoryMeasurement{},
	); err != nil {
		t.Errorf("built-in measurements violate signal-name uniqueness: %v", err)
	}
}

// Every built-in check must be feedable by a built-in measurement type.
// A check whose RequiredSignals no measurement carries can never be
// routed a measurement, so it would silently produce no verdict — this
// guards against a check landing without the measurement (or signal
// name) that feeds it.
func TestBuiltinChecksAreCovered(t *testing.T) {
	cat := catalog.New()
	if err := cat.CheckCoverage(
		cpu.CPUMeasurement{},
		gpu.GPUMeasurement{},
		node.NodeMeasurement{},
		nvlink.NVLinkMeasurement{},
		xid.XidMeasurement{},
		infiniband.InventoryMeasurement{},
	); err != nil {
		t.Errorf("built-in check has no measurement provider: %v", err)
	}
}

// CheckCoverage flags a check whose required signals no supplied
// measurement type carries — the silently-unrunnable case.
func TestCheckCoverage_DetectsUncoveredCheck(t *testing.T) {
	cat := catalog.NewEmpty()
	cat.MustRegister(health.CheckDefinition{
		ID:              "orphan_check",
		Category:        health.Node,
		RequiredSignals: []string{"no_such_signal"},
		Thresholds:      map[string]health.ThresholdSpec{},
		Rules: []health.Rule{
			{Expression: "no_such_signal > 0", Severity: health.Critical, Message: "x"},
		},
	})

	err := cat.CheckCoverage(node.NodeMeasurement{})
	if err == nil {
		t.Fatal("expected an uncovered-check error, got nil")
	}
	if !strings.Contains(err.Error(), "orphan_check") {
		t.Errorf("error = %q, want it to name the uncovered check", err.Error())
	}
}

// A check is covered only within its own category: a measurement type
// carrying the required signal names in a different category does not
// count, since routing gates on category first.
func TestCheckCoverage_RequiresMatchingCategory(t *testing.T) {
	cat := catalog.NewEmpty()
	cat.MustRegister(health.CheckDefinition{
		ID:              "node_gpu_count",
		Category:        health.Node,
		RequiredSignals: []string{"gpu_count"},
		Thresholds:      map[string]health.ThresholdSpec{},
		Rules: []health.Rule{
			{Expression: "gpu_count < 1", Severity: health.Critical, Message: "x"},
		},
	})

	// collidingCPUMeasurement carries gpu_count, but in the CPU category —
	// so it does not cover a Node-category check.
	err := cat.CheckCoverage(collidingCPUMeasurement{})
	if err == nil {
		t.Fatal("expected an uncovered-check error: gpu_count provider is in the wrong category")
	}
	if !strings.Contains(err.Error(), "node_gpu_count") {
		t.Errorf("error = %q, want it to name the uncovered check", err.Error())
	}
}

// collidingNodeMeasurement reuses gpu_count, the signal node.NodeMeasurement
// already owns in the Node category.
type collidingNodeMeasurement struct {
	Count int64 `json:"gpu_count"`
}

func (collidingNodeMeasurement) Category() health.Category           { return health.Node }
func (collidingNodeMeasurement) Errors() []health.ErroredMeasurement { return nil }

// Two measurement types in one category that share a signal name are the
// ambiguity that would let a check route to a measurement it was not
// written for; CheckSignalUniqueness must reject them.
func TestCheckSignalUniqueness_DetectsCollision(t *testing.T) {
	err := catalog.CheckSignalUniqueness(node.NodeMeasurement{}, collidingNodeMeasurement{})
	if err == nil {
		t.Fatal("expected a collision error for a shared signal name, got nil")
	}
	if !strings.Contains(err.Error(), "gpu_count") {
		t.Errorf("error = %q, want it to name the colliding signal gpu_count", err.Error())
	}
}

// A signal name reused across different categories is not a collision:
// routing is scoped by category first, so the same name in two categories
// stays unambiguous.
func TestCheckSignalUniqueness_AllowsSameNameDifferentCategory(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(
		node.NodeMeasurement{},
		collidingCPUMeasurement{},
	); err != nil {
		t.Errorf("same signal name in different categories should be allowed, got %v", err)
	}
}

// collidingCPUMeasurement carries gpu_count too, but in the CPU category.
type collidingCPUMeasurement struct {
	Count int64 `json:"gpu_count"`
}

func (collidingCPUMeasurement) Category() health.Category           { return health.CPU }
func (collidingCPUMeasurement) Errors() []health.ErroredMeasurement { return nil }
