package toolkit_test

import (
	"context"
	"testing"

	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestGPUSensor_MockSingleGPU(t *testing.T) {
	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
		gpu.GPUMeasurement{GPUID: "GPU-0"},
	))

	if sensor.Name() != "gpu" {
		t.Errorf("name = %q, want %q", sensor.Name(), "gpu")
	}

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	gm, ok := measurements[0].(gpu.GPUMeasurement)
	if !ok {
		t.Fatal("measurement is not gpu.GPUMeasurement")
	}
	if gm.GPUID != "GPU-0" {
		t.Errorf("gpu_id = %v, want GPU-0", gm.GPUID)
	}
}

func TestGPUSensor_MockMultiGPU(t *testing.T) {
	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
		gpu.GPUMeasurement{GPUID: "GPU-0"},
		gpu.GPUMeasurement{GPUID: "GPU-1"},
		gpu.GPUMeasurement{GPUID: "GPU-2"},
		gpu.GPUMeasurement{GPUID: "GPU-3"},
	))

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 4 {
		t.Fatalf("got %d measurements, want 4", len(measurements))
	}
	for i, m := range measurements {
		gm := m.(gpu.GPUMeasurement)
		expected := "GPU-" + string(rune('0'+i))
		if gm.GPUID != expected {
			t.Errorf("measurement %d: gpu_id = %v, want %v", i, gm.GPUID, expected)
		}
	}
}
