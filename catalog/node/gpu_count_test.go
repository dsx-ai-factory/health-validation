package node_test

import (
	"errors"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
)

func newCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestGPUCount_Matches(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: 4})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (4 == GB200 default expected_gpu_count)", v.Severity)
	}
}

func TestGPUCount_FewerThanExpected(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: 3})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (3 < default 4)", v.Severity)
	}
}

func TestGPUCount_MoreThanExpected(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: 5})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (5 != default 4)", v.Severity)
	}
}

// expected_gpu_count is a DefaultThreshold so operators can override
// it per fleet/SKU. Verify the override path by accepting an 8-GPU
// node when configured.
func TestGPUCount_OverrideExpected(t *testing.T) {
	cat := newCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "gpu_count",
		ThresholdName: "expected_gpu_count",
		Value:         int64(8),
	})))

	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: 8})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (8 == overridden expected)", v.Severity)
	}
}

// The check is scoped to GB200 — a non-GB200 architecture skips with
// ArchitectureUnsupported instead of running the rule.
func TestGPUCount_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("H100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: 4})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with SIGNAL_UNAVAILABLE_ARCHITECTURE; got skipped=%v reason=%v",
			v.Skipped, v.SkipReason)
	}
}

// A NodeMeasurement whose errors cover gpu_count propagates to a
// Critical verdict regardless of the count rule, since the data the
// check needed is unavailable.
func TestGPUCount_ErroredNode(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate(
		"gpu_count",
		node.ErroredNode(errors.New("nvml init failed"), node.MeasurementSignals...),
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored node)", v.Severity)
	}
}
