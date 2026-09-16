package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// IBSensor collects InfiniBand measurements through an IB backend.
//
// It is backend-agnostic scaffolding: the catalog's only built-in InfiniBand
// check is fed by [RDMALinkFlapSensor], so this one forwards both collection
// and its errored form to the consumer-supplied backend and holds no
// InfiniBand types itself. See [App.WithIBBackend].
type IBSensor struct {
	backend IBBackend
}

// NewIBSensor creates an InfiniBand sensor backed by backend.
func NewIBSensor(backend IBBackend) *IBSensor {
	return &IBSensor{backend: backend}
}

func (s *IBSensor) Name() string { return "infiniband" }

func (s *IBSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	return s.backend.MeasureIB(ctx)
}

// Errored delegates to the backend, which owns the errored measurements its
// domain's checks should see.
func (s *IBSensor) Errored(err error) []health.Measurement {
	return s.backend.Errored(err)
}
