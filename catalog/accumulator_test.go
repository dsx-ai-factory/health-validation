package catalog_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// doubledMeasurement is derived, never collected: doublingAccumulator
// produces it from a NodeMeasurement.
type doubledMeasurement struct {
	Doubled int64 `json:"doubled_gpu_count"`
}

func (doubledMeasurement) Category() health.Category           { return health.Node }
func (doubledMeasurement) Errors() []health.ErroredMeasurement { return nil }

type doublingAccumulator struct{}

func (doublingAccumulator) Name() string { return "doubling" }
func (doublingAccumulator) Outputs() []health.Measurement {
	return []health.Measurement{doubledMeasurement{}}
}

func (doublingAccumulator) Inputs() []health.Measurement {
	return []health.Measurement{node.NodeMeasurement{}}
}

func (doublingAccumulator) Accumulate(ms []health.Measurement, _ []*health.CheckDefinition) ([]health.Measurement, error) {
	var out []health.Measurement
	for _, m := range ms {
		if n, ok := m.(node.NodeMeasurement); ok {
			out = append(out, doubledMeasurement{Doubled: n.GPUCount * 2})
		}
	}
	return out, nil
}

type failingAccumulator struct{}

func (failingAccumulator) Name() string                  { return "failing" }
func (failingAccumulator) Outputs() []health.Measurement { return nil }
func (failingAccumulator) Inputs() []health.Measurement  { return nil }
func (failingAccumulator) Accumulate([]health.Measurement, []*health.CheckDefinition) ([]health.Measurement, error) {
	return nil, errors.New("boom")
}

// recordingAccumulator remembers the check IDs it was handed.
type recordingAccumulator struct{ seen *[]string }

func (recordingAccumulator) Name() string                  { return "recording" }
func (recordingAccumulator) Outputs() []health.Measurement { return nil }
func (recordingAccumulator) Inputs() []health.Measurement  { return nil }
func (r recordingAccumulator) Accumulate(_ []health.Measurement, checks []*health.CheckDefinition) ([]health.Measurement, error) {
	for _, cd := range checks {
		*r.seen = append(*r.seen, cd.ID)
	}
	return nil, nil
}

func doubledCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:              "test_doubled",
		Name:            "Test Doubled",
		Category:        health.Node,
		RequiredSignals: []string{"doubled_gpu_count"},
		Rules: []health.Rule{{
			Expression: "doubled_gpu_count > 10",
			Severity:   health.Critical,
			Message:    "doubled count too high",
		}},
	}
}

func accumulatingCatalog(t *testing.T, accs ...health.Accumulator) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"))
	cat.MustRegister(doubledCheck())
	for _, a := range accs {
		if err := cat.RegisterAccumulator(a); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

// A check reading only a derived signal is evaluated against the
// accumulator's output, as if a sensor had collected it.
func TestEvaluateAll_FeedsAccumulatorOutputsToChecks(t *testing.T) {
	cat := accumulatingCatalog(t, doublingAccumulator{})

	for _, tc := range []struct {
		gpus int64
		want health.Severity
	}{{gpus: 6, want: health.Critical}, {gpus: 4, want: health.Pass}} {
		nv, err := cat.EvaluateAll(node.NodeMeasurement{GPUCount: tc.gpus})
		if err != nil {
			t.Fatalf("EvaluateAll: %v", err)
		}
		if nv.OverallSeverity != tc.want {
			t.Errorf("gpu_count=%d: OverallSeverity = %v, want %v", tc.gpus, nv.OverallSeverity, tc.want)
		}
	}
}

// Without the accumulator nothing carries the derived signal, so the
// check is skipped rather than evaluated against something else.
func TestEvaluateAll_NoAccumulatorNoDerivedSignal(t *testing.T) {
	nv, err := accumulatingCatalog(t).EvaluateAll(node.NodeMeasurement{GPUCount: 6})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if len(nv.CheckResults) != 0 || len(nv.SkippedChecks) != 1 {
		t.Errorf("results=%d skipped=%d, want the one check skipped", len(nv.CheckResults), len(nv.SkippedChecks))
	}
}

// An accumulator fault is not a check result: evaluation fails and names
// the accumulator.
func TestEvaluateAll_AccumulatorErrorFailsEvaluation(t *testing.T) {
	_, err := accumulatingCatalog(t, failingAccumulator{}).EvaluateAll(node.NodeMeasurement{})
	if err == nil || !strings.Contains(err.Error(), "failing") {
		t.Errorf("err = %v, want an error naming the failing accumulator", err)
	}
}

// An accumulator sees every registered check, in ID order, whether or not it
// runs here: a disabled check still covers what it covers, and one scoped to
// other architectures covers codes that are not expected on this one.
func TestEvaluateAll_AccumulatorSeesEveryRegisteredCheck(t *testing.T) {
	var seen []string
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithDisabledChecks("a_disabled"))
	cat.MustRegister(doubledCheck())
	disabled := doubledCheck()
	disabled.ID = "a_disabled"
	cat.MustRegister(disabled)
	hopperOnly := doubledCheck()
	hopperOnly.ID = "b_hopper_only"
	hopperOnly.Architectures = []string{"H100"}
	cat.MustRegister(hopperOnly)
	if err := cat.RegisterAccumulator(recordingAccumulator{seen: &seen}); err != nil {
		t.Fatal(err)
	}

	if _, err := cat.EvaluateAllWithOptions(catalog.EvaluationOptions{}, node.NodeMeasurement{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a_disabled", "b_hopper_only", "test_doubled"}; !slices.Equal(seen, want) {
		t.Errorf("accumulator saw %v, want %v", seen, want)
	}
}

// notFailing has the name of failingAccumulator and none of its fault.
type notFailing struct{ failingAccumulator }

func (notFailing) Accumulate([]health.Measurement, []*health.CheckDefinition) ([]health.Measurement, error) {
	return nil, nil
}

// WithAccumulator is how a consumer configures one the catalog ships, so it
// replaces an accumulator of the same name instead of adding a second.
func TestWithAccumulator_ReplacesOneOfTheSameName(t *testing.T) {
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"),
		catalog.WithAccumulator(failingAccumulator{}), catalog.WithAccumulator(notFailing{}))
	if _, err := cat.EvaluateAll(node.NodeMeasurement{}); err != nil {
		t.Errorf("EvaluateAll = %v, want the replacement to run, not the original", err)
	}
	if err := cat.RegisterAccumulator(failingAccumulator{}); err == nil {
		t.Error("the name is still free after WithAccumulator; it should be registered once")
	}

	added := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithAccumulator(failingAccumulator{}))
	if _, err := added.EvaluateAll(node.NodeMeasurement{}); err == nil {
		t.Error("WithAccumulator for a new name did not register it")
	}
}

// A check reading a derived signal needs the accumulator's inputs collected,
// since a derived signal cannot be.
func TestCollectedSignals_MapsDerivedSignalsToTheirInputs(t *testing.T) {
	cat := accumulatingCatalog(t, doublingAccumulator{})
	cd := doubledCheck()
	got := cat.CollectedSignals(&cd)
	if !slices.Equal(got[health.Node], []string{"gpu_count"}) || len(got) != 1 {
		t.Errorf("CollectedSignals = %v, want the node measurement's gpu_count", got)
	}

	plain := health.CheckDefinition{Category: health.Node, RequiredSignals: []string{"gpu_count"}}
	got = accumulatingCatalog(t).CollectedSignals(&plain)
	if !slices.Equal(got[health.Node], []string{"gpu_count"}) {
		t.Errorf("a signal no accumulator derives stands as it is, got %v", got)
	}
}

func TestRegisterAccumulator_RejectsDuplicateName(t *testing.T) {
	cat := catalog.NewEmpty()
	if err := cat.RegisterAccumulator(doublingAccumulator{}); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterAccumulator(doublingAccumulator{}); err == nil {
		t.Error("registering the same accumulator name twice succeeded")
	}
}

// Coverage counts what registered accumulators produce: the check is
// covered by the node sensor's measurement plus the accumulator, and not
// by the sensor alone.
func TestCheckCoverage_CountsAccumulatorOutputs(t *testing.T) {
	if err := accumulatingCatalog(t, doublingAccumulator{}).CheckCoverage(node.NodeMeasurement{}); err != nil {
		t.Errorf("covered check reported uncovered: %v", err)
	}
	if err := accumulatingCatalog(t).CheckCoverage(node.NodeMeasurement{}); err == nil {
		t.Error("check reading a derived signal reported covered with no accumulator to produce it")
	}
}
