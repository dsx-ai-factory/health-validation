package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

func TestNVLinkFabric_Healthy(t *testing.T) {
	cat := fabricCatalog()
	v, err := cat.Evaluate("nvlink_fabric", healthyFabric())
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

func TestNVLinkFabric_RegistrationIncomplete(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricRegistrationIncomplete = true

	v, err := cat.Evaluate("nvlink_fabric", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
	if !strings.Contains(strings.Join(v.Messages, " "), "GPU-0") {
		t.Errorf("Messages = %q, want gpu_id interpolated", v.Messages)
	}
	if !v.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("recommended_action = %+v, want Drain(RetestHardware)", v.RecommendedAction)
	}
}

func TestNVLinkFabric_RegistrationFailed(t *testing.T) {
	cat := fabricCatalog()
	m := healthyFabric()
	m.FabricRegistrationFailed = true

	v, err := cat.Evaluate("nvlink_fabric", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
	if !strings.Contains(strings.Join(v.Messages, " "), "GPU-0") {
		t.Errorf("Messages = %q, want gpu_id interpolated", v.Messages)
	}
}

func TestNVLinkFabric_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("H100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("nvlink_fabric", healthyFabric())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}

func TestNVLinkFabric_ErroredGPU(t *testing.T) {
	cat := fabricCatalog()
	v, err := cat.Evaluate(
		"nvlink_fabric",
		nvlink.ErroredNVLink("GPU-0", errors.New("nvml fabric query failed"),
			nvlink.SignalFabricRegistrationIncomplete, nvlink.SignalFabricRegistrationFailed),
	)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored GPU)", v.Severity)
	}
}
