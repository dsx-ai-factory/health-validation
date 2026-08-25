package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// healthyCapabilities reports every required NVLink capability as
// supported on the GPU. The healthy direction is all-true, so each field
// must be set explicitly.
func healthyCapabilities() nvlink.NVLinkMeasurement {
	return nvlink.NVLinkMeasurement{
		GPUID:                        "GPU-0",
		AllLinksSupportP2P:           true,
		AllLinksSupportSysmemAccess:  true,
		AllLinksSupportP2PAtomics:    true,
		AllLinksSupportSysmemAtomics: true,
		AllLinksSupportSLI:           true,
		AllLinksSupportLink:          true,
	}
}

func TestNVLinkCapabilities_Healthy(t *testing.T) {
	cat := fabricCatalog()

	v, err := cat.Evaluate("nvlink_capabilities", healthyCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (all capabilities supported)", v.Severity)
	}
}

// Each capability has its own rule, so dropping any one capability on the
// GPU must fail Critical with a message naming the GPU and the specific
// capability.
func TestNVLinkCapabilities_CapabilityUnsupported(t *testing.T) {
	cases := []struct {
		name    string
		clear   func(*nvlink.NVLinkMeasurement)
		wantMsg string
	}{
		{"p2p", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportP2P = false }, "do not support P2P."},
		{"sysmem_access", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportSysmemAccess = false }, "system memory access"},
		{"p2p_atomics", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportP2PAtomics = false }, "P2P atomics"},
		{"sysmem_atomics", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportSysmemAtomics = false }, "system memory atomics"},
		{"sli", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportSLI = false }, "do not support SLI."},
		{"link", func(m *nvlink.NVLinkMeasurement) { m.AllLinksSupportLink = false }, "Link capability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := fabricCatalog()
			m := healthyCapabilities()
			tc.clear(&m)

			v, err := cat.Evaluate("nvlink_capabilities", m)
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != health.Critical {
				t.Errorf("severity = %v, want CRITICAL (%s unsupported)", v.Severity, tc.name)
			}
			msg := strings.Join(v.Messages, " ")
			if !strings.Contains(msg, "GPU-0") || !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("Messages = %q, want %q and gpu_id interpolated", v.Messages, tc.wantMsg)
			}
			if v.RecommendedAction != health.Drain(health.DiagnosticActionRetestHardware) {
				t.Errorf("RecommendedAction = %v, want Drain/retest_hardware", v.RecommendedAction)
			}
		})
	}
}

func TestNVLinkCapabilities_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("H100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("nvlink_capabilities", healthyCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}

// A GPU whose capability state could not be collected propagates to
// Critical regardless of the rules, since the data the check needs is
// unavailable.
func TestNVLinkCapabilities_ErroredGPU(t *testing.T) {
	cat := fabricCatalog()

	m := nvlink.ErroredNVLink("GPU-0", errors.New("nvml capability query failed"), nvlink.SignalAllLinksSupportP2P)
	v, err := cat.Evaluate("nvlink_capabilities", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored GPU)", v.Severity)
	}
	if len(v.Messages) != 1 || !strings.Contains(v.Messages[0], "measurement unavailable") {
		t.Errorf("Messages = %q, want one measurement-unavailable message", v.Messages)
	}
	if v.RecommendedAction != health.Drain(health.DiagnosticActionCheckCollection) {
		t.Errorf("RecommendedAction = %v, want Drain/check_collection", v.RecommendedAction)
	}
}

// Evaluate requires WithArchitecture; omitting it is an error, not a
// filter-off pass against the base threshold Value.
func TestNVLinkCapabilities_NoArchitectureErrors(t *testing.T) {
	cat := catalog.New(catalog.WithExecutionContext(health.OperatorInfra))

	_, err := cat.Evaluate("nvlink_capabilities", healthyCapabilities())
	if !errors.Is(err, catalog.ErrArchitectureUnset) {
		t.Fatalf("Evaluate error = %v, want ErrArchitectureUnset", err)
	}
}

// EvaluateAll is the batch path Collect uses; it evaluates an NVLink
// measurement against the NVLink check set only. A cross-category
// Dependency on this check (e.g. the Node-category gpu_count) would put a
// Node check id into the NVLink evaluation order and panic on the nil
// lookup. This guards that regression against the full builtin catalog.
func TestNVLinkCapabilities_EvaluateAllBatchPath(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// Healthy across every NVLink check (fabric, bandwidth/count/speed,
	// capabilities) so the only signal under test is that the batch path
	// completes without panicking.
	m := healthyCapabilities()
	m.LinkCount = 18
	m.SpeedMBytesPerSec = 53125

	nv, err := cat.EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity == health.Critical {
		t.Errorf("OverallSeverity = CRITICAL, want non-Critical for a fully healthy NVLink measurement")
	}
}
