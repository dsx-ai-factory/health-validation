package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// IBStatSensor collects the current state of every InfiniBand port via a
// pluggable backend. It implements [Sensor].
//
// It is the present-state counterpart to [RDMALinkFlapSensor], which reads
// the same domain's history from the journal. Their schemas are disjoint, so
// a check routes to exactly one of them.
type IBStatSensor struct {
	backend IBStatBackend
}

// NewIBStatSensor creates an InfiniBand port sensor backed by the given
// backend.
//
// For production:
//
//	sensor := toolkit.NewIBStatSensor(toolkit.IBStat())
//
// For testing, back it with [MockIBStat].
func NewIBStatSensor(backend IBStatBackend) *IBStatSensor {
	return &IBStatSensor{backend: backend}
}

func (s *IBStatSensor) Name() string { return "ibstat" }

// Measure forwards the context to the backend: ibstat is a cancellable
// command, so it honors the deadline (see [Sensor.Measure]).
func (s *IBStatSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	return s.backend.Measure(ctx)
}

// Errored reports one port of unknown identity with every signal failed, so
// a consuming check fails rather than passing on absent data. This is the
// hard-failure path (timeout or backend error); a host with no InfiniBand
// yields no measurements at all instead.
func (s *IBStatSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{
		infiniband.ErroredPort("", 0, err, infiniband.PortMeasurementSignals...),
	}
}
