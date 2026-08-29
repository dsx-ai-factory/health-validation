package gpu_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func healthyThrottleGB200() gpu.GPUMeasurement {
	return gpu.GPUMeasurement{GPUID: "GPU-0"}
}

func throttleCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	c := catalog.NewEmpty(append(defaults, opts...)...)
	c.MustRegister(gpu.GPUSensorThrottle())
	return c
}

func TestGPUSensorThrottle(t *testing.T) {
	tests := []struct {
		name           string
		cat            *catalog.Catalog
		measurement    func() gpu.GPUMeasurement
		wantSeverity   health.Severity
		wantSkipped    bool
		wantSkipReason health.SkipReason
		wantAction     health.RecommendedAction
	}{
		{
			name:         "healthy",
			cat:          throttleCatalog(),
			measurement:  healthyThrottleGB200,
			wantSeverity: health.Pass,
		},
		{
			name: "hardware slowdown",
			cat:  throttleCatalog(),
			measurement: func() gpu.GPUMeasurement {
				m := healthyThrottleGB200()
				m.HWSlowdown = true
				return m
			},
			wantSeverity: health.Critical,
			wantAction:   health.GenericDrain(),
		},
		{
			name: "thermal slowdown",
			cat:  throttleCatalog(),
			measurement: func() gpu.GPUMeasurement {
				m := healthyThrottleGB200()
				m.HWThermalSlowdown = true
				return m
			},
			wantSeverity: health.Critical,
		},
		{
			name: "power-brake slowdown",
			cat:  throttleCatalog(),
			measurement: func() gpu.GPUMeasurement {
				m := healthyThrottleGB200()
				m.HWPowerBrakeSlowdown = true
				return m
			},
			wantSeverity: health.Critical,
		},
		{
			name:           "skipped on wrong architecture",
			cat:            throttleCatalog(catalog.WithArchitecture("T4")),
			measurement:    healthyThrottleGB200,
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat.Evaluate("gpu_sensor_throttle", tt.measurement())
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
				t.Errorf("recommended_action = %v, want %v", v.RecommendedAction, tt.wantAction)
			}
			if tt.wantSeverity == health.Pass && !v.RecommendedAction.IsZero() {
				t.Errorf("recommended_action = %v, want zero on PASS", v.RecommendedAction)
			}
		})
	}
}
