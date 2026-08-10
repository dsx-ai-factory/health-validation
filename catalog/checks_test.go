package catalog_test

import (
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
)

func TestNew_HasBuiltinChecks(t *testing.T) {
	cat := catalog.New()
	if cat.Len() == 0 {
		t.Fatal("New() created catalog with no checks")
	}
}

func TestNew_UniqueIDs(t *testing.T) {
	cat := catalog.New()
	checks := cat.List(catalog.Filter{})
	seen := make(map[string]bool)
	for _, cd := range checks {
		if cd.ID == "" {
			t.Error("check has empty ID")
		}
		if seen[cd.ID] {
			t.Errorf("duplicate check ID %q", cd.ID)
		}
		seen[cd.ID] = true
	}
}

func TestNew_FilterByCategory(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	xid := health.Xid
	xidChecks := cat.List(catalog.Filter{Category: &xid})
	if len(xidChecks) == 0 {
		t.Error("expected Xid checks in auto-loaded catalog")
	}
}

func TestNew_RegistersNVLinkActive(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_active"); err != nil {
		t.Errorf("Get(nvlink_active) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersGPUPowerState(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("gpu_power_state"); err != nil {
		t.Errorf("Get(gpu_power_state) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersCPUCount(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("cpu_count"); err != nil {
		t.Errorf("Get(cpu_count) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersNUMANodeCount(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("numa_node_count"); err != nil {
		t.Errorf("Get(numa_node_count) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersNVLinkCapabilities(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_capabilities"); err != nil {
		t.Errorf("Get(nvlink_capabilities) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersNVLinkFabric(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_fabric"); err != nil {
		t.Errorf("Get(nvlink_fabric) = %v, want check to ship in builtinChecks()", err)
	}
}
