package node_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

func newCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestGPUCount(t *testing.T) {
	tests := []struct {
		name           string
		cat            func() *catalog.Catalog
		measurement    func() node.NodeMeasurement
		wantSeverity   health.Severity
		wantSkipped    bool
		wantSkipReason health.SkipReason
	}{
		{
			name:         "matches default expected count",
			cat:          func() *catalog.Catalog { return newCatalog() },
			measurement:  func() node.NodeMeasurement { return node.NodeMeasurement{GPUCount: 4} },
			wantSeverity: health.Pass,
		},
		{
			name:         "fewer than expected",
			cat:          func() *catalog.Catalog { return newCatalog() },
			measurement:  func() node.NodeMeasurement { return node.NodeMeasurement{GPUCount: 3} },
			wantSeverity: health.Critical,
		},
		{
			name:         "more than expected",
			cat:          func() *catalog.Catalog { return newCatalog() },
			measurement:  func() node.NodeMeasurement { return node.NodeMeasurement{GPUCount: 5} },
			wantSeverity: health.Critical,
		},
		{
			// expected_gpu_count is a DefaultThreshold so operators can
			// override it per fleet/SKU. Verify the override path by
			// accepting an 8-GPU node when configured.
			name: "override accepts the configured count",
			cat: func() *catalog.Catalog {
				return newCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
					CheckID:       "gpu_count",
					ThresholdName: "expected_gpu_count",
					Value:         int64(8),
				})))
			},
			measurement:  func() node.NodeMeasurement { return node.NodeMeasurement{GPUCount: 8} },
			wantSeverity: health.Pass,
		},
		{
			// The check is scoped to GB200 -- a non-GB200 architecture
			// skips with ArchitectureUnsupported instead of running the
			// rule.
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("H100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    func() node.NodeMeasurement { return node.NodeMeasurement{GPUCount: 4} },
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// A NodeMeasurement whose errors cover gpu_count propagates to
			// a Critical verdict regardless of the count rule, since the
			// data the check needed is unavailable.
			name: "errored node forces critical",
			cat:  func() *catalog.Catalog { return newCatalog() },
			measurement: func() node.NodeMeasurement {
				return node.ErroredNode(errors.New("nvml init failed"), node.MeasurementSignals...)
			},
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("gpu_count", tt.measurement())
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if v.Skipped != tt.wantSkipped || v.SkipReason != tt.wantSkipReason {
				t.Fatalf("skipped=%v reason=%v, want skipped=%v reason=%v", v.Skipped, v.SkipReason, tt.wantSkipped, tt.wantSkipReason)
			}
			if tt.wantSkipped {
				return
			}
			if v.Severity != tt.wantSeverity {
				t.Errorf("severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
		})
	}
}
