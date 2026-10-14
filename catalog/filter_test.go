package catalog_test

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// cadenceCheck builds a minimal registrable check with the given id and cadence.
func cadenceCheck(id string, cadence health.SchedulingCadence) health.CheckDefinition {
	return health.CheckDefinition{
		ID:                id,
		Category:          health.Node,
		SchedulingCadence: cadence,
		RequiredSignals:   []string{"gpu_count"},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules:             []health.Rule{{Expression: "gpu_count > 0", Severity: health.Critical, Message: "x"}},
	}
}

func checkIDs(defs []*health.CheckDefinition) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.ID
	}
	return out
}

// Filtering by cadence returns every check whose declared cadence is at least
// as frequent as the requested one, because cadences nest
// (Continuous > Prolog > MaintenanceWindow).
func TestListFiltersByCadenceHierarchy(t *testing.T) {
	cat := catalog.NewEmpty()
	cat.MustRegister(cadenceCheck("cont", health.Continuous))
	cat.MustRegister(cadenceCheck("prolog", health.Prolog))
	cat.MustRegister(cadenceCheck("maint", health.MaintenanceWindow))

	tests := []struct {
		name    string
		want    []string // sorted: List sorts by ID
		cadence health.SchedulingCadence
	}{
		{name: "continuous returns only continuous", cadence: health.Continuous, want: []string{"cont"}},
		{name: "prolog returns continuous and prolog", cadence: health.Prolog, want: []string{"cont", "prolog"}},
		{name: "maintenance returns every check", cadence: health.MaintenanceWindow, want: []string{"cont", "maint", "prolog"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cadence := tt.cadence
			got := checkIDs(cat.List(catalog.Filter{Cadence: &cadence}))
			if !slices.Equal(got, tt.want) {
				t.Errorf("List(cadence=%v) = %v, want %v", tt.cadence, got, tt.want)
			}
		})
	}

	// A nil cadence filter is ignored: every check is returned.
	if got := len(cat.List(catalog.Filter{})); got != 3 {
		t.Errorf("List(no filter) returned %d checks, want 3", got)
	}
}

func cadenceCatalog() *catalog.Catalog {
	cat := catalog.NewEmpty(catalog.WithArchitecture(catalogtest.DummyArchitecture))
	cat.MustRegister(cadenceCheck("cont", health.Continuous))
	cat.MustRegister(cadenceCheck("prolog", health.Prolog))
	cat.MustRegister(cadenceCheck("maint", health.MaintenanceWindow))
	return cat
}

func evaluatedVerdicts(t *testing.T, cat *catalog.Catalog, cadence *health.SchedulingCadence) map[string]health.CheckVerdict {
	t.Helper()
	nv, err := cat.EvaluateAllWithOptions(
		catalog.EvaluationOptions{Cadence: cadence},
		node.NodeMeasurement{GPUCount: 1},
	)
	if err != nil {
		t.Fatalf("EvaluateAllWithOptions: %v", err)
	}
	got := make(map[string]health.CheckVerdict, len(nv.CheckResults)+len(nv.SkippedChecks))
	for _, v := range nv.CheckResults {
		got[v.CheckID] = v
	}
	for _, v := range nv.SkippedChecks {
		got[v.CheckID] = v
	}
	return got
}

func TestEvaluateAllWithOptions_SkipsByCadenceHierarchy(t *testing.T) {
	cat := cadenceCatalog()
	tests := []struct {
		name        string
		wantSkipped []string
		cadence     health.SchedulingCadence
	}{
		{name: "continuous skips prolog and maintenance", cadence: health.Continuous, wantSkipped: []string{"maint", "prolog"}},
		{name: "prolog skips maintenance", cadence: health.Prolog, wantSkipped: []string{"maint"}},
		{name: "maintenance skips none", cadence: health.MaintenanceWindow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.cadence
			got := evaluatedVerdicts(t, cat, &c)
			var skipped []string
			for id, v := range got {
				if v.Skipped {
					if v.SkipReason != health.CadenceMismatch {
						t.Errorf("%s skip reason = %v, want CADENCE_MISMATCH", id, v.SkipReason)
					}
					skipped = append(skipped, id)
				}
			}
			slices.Sort(skipped)
			if !slices.Equal(skipped, tt.wantSkipped) {
				t.Errorf("skipped at cadence %v = %v, want %v", tt.cadence, skipped, tt.wantSkipped)
			}
		})
	}

	for id, v := range evaluatedVerdicts(t, cat, nil) {
		if v.Skipped {
			t.Errorf("EvaluateAll unset cadence skipped %s with %v", id, v.SkipReason)
		}
	}
}

func TestEvaluate_IgnoresCadenceOptions(t *testing.T) {
	cat := cadenceCatalog()
	v, err := cat.Evaluate("prolog", node.NodeMeasurement{GPUCount: 1})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Skipped {
		t.Fatalf("Evaluate(prolog) skipped with %v; single-ID Evaluate is not cadence-filtered", v.SkipReason)
	}
}
