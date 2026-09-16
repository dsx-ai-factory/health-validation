package toolkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

func linkFlapCatalog() *catalog.Catalog {
	return catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
}

func TestRDMALinkFlapSensor_Name(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap())
	if s.Name() != "rdma_linkflap" {
		t.Errorf("Name() = %q, want rdma_linkflap", s.Name())
	}
}

func TestRDMALinkFlapSensor_Measure(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap(twoOutageLines...))
	ms, err := s.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1 aggregate", len(ms))
	}
	m, ok := ms[0].(infiniband.LinkFlapMeasurement)
	if !ok {
		t.Fatalf("measurement is %T, want infiniband.LinkFlapMeasurement", ms[0])
	}
	if len(m.OutageDurations) != 2 {
		t.Errorf("OutageDurations = %v, want two outages", m.OutageDurations)
	}
}

// End-to-end against the real catalog check: two outages ≥ the 30s default
// window trip rdma_linkflap_fatal (default threshold 1) to CRITICAL.
func TestRDMALinkFlapSensor_FatalFlap(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap(twoOutageLines...))
	nv, err := toolkit.Collect(linkFlapCatalog(), nil, s)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
}

// Only "Link up" lines: no outage, check passes and is not skipped.
func TestRDMALinkFlapSensor_CleanBoot(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap(onlyUpLines...))
	nv, err := toolkit.Collect(linkFlapCatalog(), nil, s)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
}

// A brief outage below the window is not fatal → PASS.
func TestRDMALinkFlapSensor_ShortOutagePasses(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap(
		"Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:49:10 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	))
	nv, err := toolkit.Collect(linkFlapCatalog(), nil, s)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("OverallSeverity = %v, want PASS (10s < 30s window)", nv.OverallSeverity)
	}
}

// Errored reports the journal unreadable, failing the check Critical.
func TestRDMALinkFlapSensor_Errored(t *testing.T) {
	s := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap())
	ms := s.Errored(errors.New("journal unreadable"))
	if len(ms) != 1 {
		t.Fatalf("Errored returned %d measurements, want 1", len(ms))
	}
	nv, err := toolkit.Collect(linkFlapCatalog(), nil, erroredSensor{ms: ms})
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL on unreadable journal", nv.OverallSeverity)
	}
}

// erroredSensor emits pre-built errored measurements, standing in for a
// sensor whose collection failed.
type erroredSensor struct {
	ms []health.Measurement
}

func (erroredSensor) Name() string { return "rdma_linkflap" }
func (e erroredSensor) Measure(context.Context) ([]health.Measurement, error) {
	return e.ms, nil
}
func (e erroredSensor) Errored(error) []health.Measurement { return e.ms }
