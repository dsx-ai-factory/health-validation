package toolkit

import (
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
)

// NodeBackend supplies the host-wide measurement consumed by the Node sensor:
// a single [node.NodeMeasurement] (gpu_count, …) wrapped in [health.Measurement].
// When the data source itself is unobservable the implementation should return
// a [node.ErroredNode] so node-level checks fail rather than silently dropping
// the host from the verdict.
//
// It is one of the three narrow, per-domain backend interfaces ([GPUBackend],
// [NVLinkBackend]); a full multi-domain source (e.g. the nvml backend)
// implements all three.
type NodeBackend interface {
	MeasureNode() ([]health.Measurement, error)
}

// MockNode returns a [NodeBackend] that emits the provided node measurement.
//
//	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
//	    node.NodeMeasurement{GPUCount: 8},
//	))
func MockNode(m node.NodeMeasurement) NodeBackend {
	return mockNodeBackend{measurements: []health.Measurement{m}}
}

type mockNodeBackend struct {
	measurements []health.Measurement
}

func (m mockNodeBackend) MeasureNode() ([]health.Measurement, error) {
	return m.measurements, nil
}
