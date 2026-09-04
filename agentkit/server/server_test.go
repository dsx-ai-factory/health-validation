package server

import (
	"context"
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type testSensor struct {
	measure func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error)
	id      string
	signals collect.Signals
}

func (sensor testSensor) ID() string { return sensor.id }
func (sensor testSensor) Signals() collect.Signals {
	return sensor.signals
}

func (sensor testSensor) Measure(ctx context.Context, options ...collect.OpOption) (*observation.ObservationBatch, error) {
	return sensor.measure(ctx, options...)
}

func TestCheckAttributesTopLevelErrorsToSensor(t *testing.T) {
	collector := New()
	collector.Register(testSensor{
		id:      "gpu.dcgm",
		signals: collect.Signals{gpusignals.PowerDraw, gpusignals.TemperatureCelsius},
		measure: func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			return nil, errors.New("DCGM unavailable")
		},
	})

	batch, err := collector.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if batch.GetCollectionId() == "" {
		t.Fatal("Check() returned an empty collection ID")
	}
	if len(batch.GetObservations()) != 2 {
		t.Fatalf("Check() returned %d observations, want 2", len(batch.GetObservations()))
	}
	for _, current := range batch.GetObservations() {
		if current.GetSource() != "gpu.dcgm" {
			t.Errorf("observation source = %q, want %q", current.GetSource(), "gpu.dcgm")
		}
		failure := current.GetCollectionError()
		if failure == nil {
			t.Fatalf("signal %q has no collection error", current.GetSignalId())
		}
		if failure.GetSource() != "gpu.dcgm" {
			t.Errorf("collection error source = %q, want %q", failure.GetSource(), "gpu.dcgm")
		}
	}
}

func TestCheckRejectsObservationWithoutOutcome(t *testing.T) {
	collector := New()
	collector.Register(testSensor{
		id:      "gpu.dcgm",
		signals: collect.Signals{gpusignals.PowerDraw},
		measure: func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			return &observation.ObservationBatch{Observations: []*observation.Observation{
				{SignalId: gpusignals.PowerDraw.ID()},
			}}, nil
		},
	})

	batch, err := collector.Check(context.Background())
	if err == nil {
		t.Fatal("Check() returned nil error for an observation without an outcome")
	}
	if batch != nil {
		t.Fatalf("Check() batch = %v, want nil", batch)
	}
}
