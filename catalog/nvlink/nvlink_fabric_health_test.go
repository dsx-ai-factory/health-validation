package nvlink_test

import (
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
)

func healthyFabric() nvlink.NVLinkMeasurement {
	return nvlink.NVLinkMeasurement{GPUID: "GPU-0"}
}

func fabricCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestNVLinkFabricHealth_Healthy(t *testing.T) {
	cat := fabricCatalog()
	v, err := cat.Evaluate("nvlink_fabric_health", healthyFabric())
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS", v.Severity)
	}
	if !v.NVIDIAEndorsed {
		t.Error("expected nvidia_endorsed = true")
	}
}

func TestNVLinkFabricHealth_BandwidthDegraded(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricBandwidthDegraded = true

	v, err := cat.Evaluate("nvlink_fabric_health", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestNVLinkFabricHealth_RouteRecoveryInProgress(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricRouteRecoveryInProgress = true

	v, err := cat.Evaluate("nvlink_fabric_health", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestNVLinkFabricHealth_RouteUnhealthy(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricRouteUnhealthy = true

	v, err := cat.Evaluate("nvlink_fabric_health", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestNVLinkFabricHealth_AccessTimeoutRecovery(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricAccessTimeoutRecovery = true

	v, err := cat.Evaluate("nvlink_fabric_health", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestNVLinkFabricHealth_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("H100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("nvlink_fabric_health", healthyFabric())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}
