package catalog_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// driverEchoMeasurement derives the driver version it was given, so a
// rule can observe whether the catalog's host reached the derivation.
type driverEchoMeasurement struct {
	Count int64 `json:"echo_count"`
}

func (driverEchoMeasurement) Category() health.Category           { return health.Node }
func (driverEchoMeasurement) Errors() []health.ErroredMeasurement { return nil }
func (driverEchoMeasurement) DerivedSignals(host health.Host) map[string]any {
	return map[string]any{"seen_driver": host.DriverVersion.Raw}
}

var _ health.DerivedSignaler = driverEchoMeasurement{}

func driverEchoCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:              "test_driver_echo",
		Name:            "Test Driver Echo",
		Category:        health.Node,
		RequiredSignals: []string{"seen_driver"},
		Rules: []health.Rule{{
			Expression: `seen_driver == "575.51.03"`,
			Severity:   health.Critical,
			Message:    "derivation saw the configured driver version",
		}},
	}
}

func echoCatalog(t *testing.T, opts ...catalog.Option) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(append([]catalog.Option{catalog.WithArchitecture("GB200")}, opts...)...)
	cat.MustRegister(driverEchoCheck())
	return cat
}

// TestWithDriverVersion_ReachesDerivedSignals covers both evaluation
// paths. The batch path extracts signals once per measurement, before any
// check's Params exist, and so is handed the host separately from the
// single-check path; the two must not drift apart.
func TestWithDriverVersion_ReachesDerivedSignals(t *testing.T) {
	v, ok := health.ParseDriverVersion("575.51.03")
	if !ok {
		t.Fatal("ParseDriverVersion failed on a well-formed version")
	}
	cat := echoCatalog(t, catalog.WithDriverVersion(v))

	nv, err := cat.EvaluateAll(driverEchoMeasurement{})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("batch path: OverallSeverity = %v, want CRITICAL (derivation did not see the version)", nv.OverallSeverity)
	}

	single, err := cat.Evaluate("test_driver_echo", driverEchoMeasurement{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if single.Severity != health.Critical {
		t.Errorf("single-check path: Severity = %v, want CRITICAL (derivation did not see the version)", single.Severity)
	}
}

// Without the option the version is unknown, and a derivation sees the
// zero value rather than anything guessed.
func TestWithDriverVersion_AbsentIsZero(t *testing.T) {
	nv, err := echoCatalog(t).EvaluateAll(driverEchoMeasurement{})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("OverallSeverity = %v, want PASS with no driver version configured", nv.OverallSeverity)
	}
}
