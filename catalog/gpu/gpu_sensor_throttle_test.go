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

func TestGPUSensorThrottle_Healthy(t *testing.T) {
	cat := throttleCatalog()
	v, err := cat.Evaluate("gpu_sensor_throttle", healthyThrottleGB200())
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS", v.Severity)
	}
	if !v.RecommendedAction.IsZero() {
		t.Errorf("recommended_action = %v, want zero on PASS", v.RecommendedAction)
	}
}

func TestGPUSensorThrottle_HWSlowdown(t *testing.T) {
	cat := throttleCatalog()
	m := healthyThrottleGB200()
	m.HWSlowdown = true

	v, err := cat.Evaluate("gpu_sensor_throttle", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("recommended_action = %v, want Drain(RetestHardware)", v.RecommendedAction)
	}
}

func TestGPUSensorThrottle_HWThermalSlowdown(t *testing.T) {
	cat := throttleCatalog()
	m := healthyThrottleGB200()
	m.HWThermalSlowdown = true

	v, err := cat.Evaluate("gpu_sensor_throttle", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestGPUSensorThrottle_HWPowerBrakeSlowdown(t *testing.T) {
	cat := throttleCatalog()
	m := healthyThrottleGB200()
	m.HWPowerBrakeSlowdown = true

	v, err := cat.Evaluate("gpu_sensor_throttle", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", v.Severity)
	}
}

func TestGPUSensorThrottle_SkippedOnWrongArchitecture(t *testing.T) {
	cat := throttleCatalog(catalog.WithArchitecture("T4"))

	v, err := cat.Evaluate("gpu_sensor_throttle", healthyThrottleGB200())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}
