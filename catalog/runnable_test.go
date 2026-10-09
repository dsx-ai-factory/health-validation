package catalog_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// RunnableChecks must exclude exactly the checks EvaluateAllWithOptions skips
// from the check and evaluation context alone. Every check reads the same
// signal, so with one measurement carrying it the runnable checks are exactly
// the evaluated ones. Dependencies are left out: RunnableChecks ignores them
// (see TestRunnableChecks_KeepsDependents).
func TestRunnableChecks_MatchesEvaluateAllSkips(t *testing.T) {
	check := func(id string, mutate func(*health.CheckDefinition)) health.CheckDefinition {
		cd := health.CheckDefinition{
			ID:                id,
			Category:          health.Node,
			RequiredSignals:   []string{"first"},
			ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
			Thresholds:        map[string]health.ThresholdSpec{},
			Rules:             []health.Rule{{Expression: "false", Severity: health.Critical, Message: id}},
		}
		if mutate != nil {
			mutate(&cd)
		}
		return cd
	}
	defs := []health.CheckDefinition{
		check("runs", nil),
		check("disabled", nil),
		check("maintenance_only", func(cd *health.CheckDefinition) { cd.SchedulingCadence = health.MaintenanceWindow }),
		check("other_architecture", func(cd *health.CheckDefinition) { cd.Architectures = []string{"not-this-one"} }),
		check("tenant_only", func(cd *health.CheckDefinition) {
			cd.ExecutionContexts = []health.ExecutionContext{health.TenantGuest}
		}),
		check("unconfigured_threshold", func(cd *health.CheckDefinition) {
			cd.Thresholds = map[string]health.ThresholdSpec{"limit": {RequiredOverride: true}}
		}),
	}
	cat := catalog.NewEmpty(
		catalog.WithArchitecture(catalogtest.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithDisabledChecks("disabled"),
	)
	for _, cd := range defs {
		cat.MustRegister(cd)
	}
	cadence := health.Continuous
	opts := catalog.EvaluationOptions{Cadence: &cadence}

	runnable, err := cat.RunnableChecks(opts)
	if err != nil {
		t.Fatalf("RunnableChecks: %v", err)
	}
	var got []string
	for _, cd := range runnable {
		got = append(got, cd.ID)
	}
	slices.Sort(got)

	nv, err := cat.EvaluateAllWithOptions(opts, twoSignalMeasurement{})
	if err != nil {
		t.Fatalf("EvaluateAllWithOptions: %v", err)
	}
	var want []string
	for _, v := range nv.CheckResults {
		want = append(want, v.CheckID)
	}
	slices.Sort(want)
	reasons := map[health.SkipReason]bool{}
	for _, v := range nv.SkippedChecks {
		reasons[v.SkipReason] = true
	}

	if !slices.Equal(got, want) {
		t.Errorf("RunnableChecks = %v, want the evaluated checks %v", got, want)
	}
	// Guard the fixture: every measurement-independent reason must occur, or
	// the comparison above stops covering it.
	for _, r := range []health.SkipReason{
		health.CheckDisabled, health.CadenceMismatch,
		health.ArchitectureUnsupported, health.DeploymentUnsupported, health.ThresholdUnconfigured,
	} {
		if !reasons[r] {
			t.Errorf("fixture produced no %v skip; reasons seen: %v", r, slices.Collect(maps.Keys(reasons)))
		}
	}
}

// Collection must not depend on whether evaluation cascades a skipped
// dependency, so a dependent of a disabled check stays runnable.
func TestRunnableChecks_KeepsDependents(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture(catalogtest.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithDisabledChecks("disabled"),
	)
	for _, cd := range []health.CheckDefinition{
		{ID: "disabled", Category: health.Node, RequiredSignals: []string{"first"}},
		{ID: "dependent", Category: health.Node, RequiredSignals: []string{"first"}, Dependencies: []string{"disabled"}},
	} {
		cd.ExecutionContexts = []health.ExecutionContext{health.OperatorInfra}
		cd.Rules = []health.Rule{{Expression: "false", Severity: health.Critical, Message: cd.ID}}
		cat.MustRegister(cd)
	}
	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{})
	if err != nil {
		t.Fatalf("RunnableChecks: %v", err)
	}
	if len(runnable) != 1 || runnable[0].ID != "dependent" {
		t.Fatalf("RunnableChecks = %v, want only dependent", runnable)
	}
}

func TestRunnableChecks_ArchitectureUnset(t *testing.T) {
	if _, err := catalog.New().RunnableChecks(catalog.EvaluationOptions{}); err == nil {
		t.Fatal("RunnableChecks without an architecture: want error")
	}
}
