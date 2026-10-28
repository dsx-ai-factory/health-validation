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

func (doublingAccumulator) Accumulate(ms []health.Measurement) ([]health.Measurement, error) {
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
func (failingAccumulator) Accumulate([]health.Measurement) ([]health.Measurement, error) {
	return nil, errors.New("boom")
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

// A collector cannot gather a derived signal, so CollectedSignals names
// what the accumulator reads in its place, and leaves a collected signal
// as it is.
func TestCollectedSignals_ReplacesDerivedWithInputs(t *testing.T) {
	cat := accumulatingCatalog(t, doublingAccumulator{})
	derived := doubledCheck()
	got := cat.CollectedSignals(&derived)
	if _, ok := got[health.Node]; !ok || slices.Contains(got[health.Node], "doubled_gpu_count") || !slices.Contains(got[health.Node], node.SignalGPUCount) {
		t.Errorf("CollectedSignals(derived) = %v, want the node measurement's signals in place of doubled_gpu_count", got)
	}

	collected := health.CheckDefinition{ID: "plain", Category: health.Node, RequiredSignals: []string{node.SignalGPUCount}}
	if got := cat.CollectedSignals(&collected); !slices.Equal(got[health.Node], []string{node.SignalGPUCount}) || len(got) != 1 {
		t.Errorf("CollectedSignals(collected) = %v, want only %s", got, node.SignalGPUCount)
	}
}
