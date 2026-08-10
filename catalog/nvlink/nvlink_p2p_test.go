package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
)

func TestNVLinkP2P_AllReachable(t *testing.T) {
	cat := fabricCatalog()

	v, err := cat.Evaluate("nvlink_p2p", nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (0 unreachable == default budget)", v.Severity)
	}
}

func TestNVLinkP2P_OneUnreachable(t *testing.T) {
	cat := fabricCatalog()

	v, err := cat.Evaluate("nvlink_p2p", nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (1 > default 0)", v.Severity)
	}
	if v.RecommendedAction != health.Drain(health.DiagnosticActionContactSupport) {
		t.Errorf("RecommendedAction = %v, want contact_support", v.RecommendedAction)
	}
	msg := strings.Join(v.Messages, " ")
	for _, want := range []string{"GPU-0", "1 peer", "allowed: 0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
		}
	}
}

func TestNVLinkP2P_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("T4"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("nvlink_p2p", nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}

// A GPU whose P2P state could not be collected propagates to Critical
// regardless of the rule, since the data the check needs is unavailable.
func TestNVLinkP2P_ErroredGPU(t *testing.T) {
	cat := fabricCatalog()

	m := nvlink.ErroredNVLink("GPU-0", errors.New("nvml p2p status query failed"), nvlink.SignalUnhealthyP2PPeerCount)
	v, err := cat.Evaluate("nvlink_p2p", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored GPU)", v.Severity)
	}
}
