package toolkit_test

import (
	"context"
	"testing"

	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestNodeSensor_MockEmits(t *testing.T) {
	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 8},
	))

	if sensor.Name() != "node" {
		t.Errorf("name = %q, want %q", sensor.Name(), "node")
	}

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	nm, ok := measurements[0].(node.NodeMeasurement)
	if !ok {
		t.Fatal("measurement is not NodeMeasurement")
	}
	if nm.GPUCount != 8 {
		t.Errorf("GPUCount = %d, want 8", nm.GPUCount)
	}
}
