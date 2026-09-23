package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

func TestNVLinkActive(t *testing.T) {
	tests := []struct {
		cat              func() *catalog.Catalog
		measurement      func() nvlink.NVLinkMeasurement
		name             string
		wantMessageParts []string
		wantAction       health.RecommendedAction
		wantSeverity     health.Severity
		wantSkipReason   health.SkipReason
		wantSkipped      bool
	}{
		{
			name:         "all active",
			cat:          func() *catalog.Catalog { return fabricCatalog() },
			measurement:  func() nvlink.NVLinkMeasurement { return nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 0} },
			wantSeverity: health.Pass,
		},
		{
			name:             "one inactive exceeds default budget",
			cat:              func() *catalog.Catalog { return fabricCatalog() },
			measurement:      func() nvlink.NVLinkMeasurement { return nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 1} },
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "1 inactive", "allowed: 0"},
		},
		{
			// max_inactive_nvlinks is a DefaultThreshold so operators can
			// carve out a per-GPU tolerated budget.
			name: "override allows budget",
			cat: func() *catalog.Catalog {
				return fabricCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
					CheckID:       "nvlink_active",
					ThresholdName: "max_inactive_nvlinks",
					Value:         int64(2),
				})))
			},
			measurement:  func() nvlink.NVLinkMeasurement { return nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 2} },
			wantSeverity: health.Pass,
		},
		{
			name: "override budget still has a ceiling",
			cat: func() *catalog.Catalog {
				return fabricCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
					CheckID:       "nvlink_active",
					ThresholdName: "max_inactive_nvlinks",
					Value:         int64(2),
				})))
			},
			measurement:  func() nvlink.NVLinkMeasurement { return nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 3} },
			wantSeverity: health.Critical,
		},
		{
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("V100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    func() nvlink.NVLinkMeasurement { return nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 0} },
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// A GPU whose link state could not be collected propagates to
			// Critical regardless of the rule, since the data the check
			// needs is unavailable.
			name: "errored GPU forces critical",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.ErroredNVLink("GPU-0", errors.New("nvml link-state query failed"), nvlink.SignalInactiveCount)
			},
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("nvlink_active", tt.measurement())
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped != tt.wantSkipped || v.SkipReason != tt.wantSkipReason {
				t.Fatalf("skipped=%v reason=%v, want skipped=%v reason=%v", v.Skipped, v.SkipReason, tt.wantSkipped, tt.wantSkipReason)
			}
			if tt.wantSkipped {
				return
			}
			if v.Severity != tt.wantSeverity {
				t.Fatalf("severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !tt.wantAction.IsZero() && !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.wantAction)
			}
			msg := strings.Join(v.Messages, " ")
			for _, want := range tt.wantMessageParts {
				if !strings.Contains(msg, want) {
					t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
				}
			}
		})
	}
}
