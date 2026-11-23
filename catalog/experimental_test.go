// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func expFixture(id string, maturity health.MaturityLevel, architectures ...string) health.CheckDefinition {
	return health.CheckDefinition{
		ID:                id,
		Category:          health.Node,
		RequiredSignals:   []string{"first"},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Architectures:     architectures,
		MaturityLevel:     maturity,
		Rules:             []health.Rule{{Expression: "false", Severity: health.Critical, Message: id}},
	}
}

func expCatalog(defs []health.CheckDefinition, opts ...catalog.Option) *catalog.Catalog {
	cat := catalog.NewEmpty(append([]catalog.Option{
		catalog.WithArchitecture(catalogtest.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	}, opts...)...)
	for _, cd := range defs {
		cat.MustRegister(cd)
	}
	return cat
}

func expDefs() []health.CheckDefinition {
	return []health.CheckDefinition{
		expFixture("stable", health.Stable),
		expFixture("experimental_a", health.Experimental),
		expFixture("experimental_b", health.Experimental),
	}
}

// expOutcome evaluates one measurement and returns the IDs that ran and the
// skip verdicts by ID.
func expOutcome(t *testing.T, cat *catalog.Catalog) ([]string, map[string]health.CheckVerdict) {
	t.Helper()
	nv, err := cat.EvaluateAll(twoSignalMeasurement{First: 1})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	var ran []string
	for _, v := range nv.CheckResults {
		ran = append(ran, v.CheckID)
	}
	slices.Sort(ran)
	skipped := map[string]health.CheckVerdict{}
	for _, v := range nv.SkippedChecks {
		skipped[v.CheckID] = v
	}
	return ran, skipped
}

// An experimental check is off unless it is enabled, so a stable catalog gains
// no checks it did not ask for. Enabling is per check or for all of them, and
// an operator's disable still wins.
func TestExperimentalChecks_OffUnlessEnabled(t *testing.T) {
	tests := []struct {
		name        string
		opts        []catalog.Option
		wantRan     []string
		wantSkipped []string
	}{
		{
			name:        "default",
			wantRan:     []string{"stable"},
			wantSkipped: []string{"experimental_a", "experimental_b"},
		},
		{
			name:    "all enabled",
			opts:    []catalog.Option{catalog.WithAllExperimentalChecks()},
			wantRan: []string{"experimental_a", "experimental_b", "stable"},
		},
		{
			name:        "one enabled",
			opts:        []catalog.Option{catalog.WithExperimentalChecks("experimental_a")},
			wantRan:     []string{"experimental_a", "stable"},
			wantSkipped: []string{"experimental_b"},
		},
		{
			// A check that graduates to stable must keep working in a config
			// that already lists it, and an unknown ID is the caller's to reject.
			name:        "naming a stable or unknown check changes nothing",
			opts:        []catalog.Option{catalog.WithExperimentalChecks("stable", "no_such_check")},
			wantRan:     []string{"stable"},
			wantSkipped: []string{"experimental_a", "experimental_b"},
		},
		{
			name:        "an operator disable beats enabling all",
			opts:        []catalog.Option{catalog.WithAllExperimentalChecks(), catalog.WithDisabledChecks("experimental_a")},
			wantRan:     []string{"experimental_b", "stable"},
			wantSkipped: []string{"experimental_a"},
		},
		{
			name:        "an operator disable beats enabling one",
			opts:        []catalog.Option{catalog.WithExperimentalChecks("experimental_a"), catalog.WithDisabledChecks("experimental_a")},
			wantRan:     []string{"stable"},
			wantSkipped: []string{"experimental_a", "experimental_b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat := expCatalog(expDefs(), tc.opts...)
			ran, skipped := expOutcome(t, cat)
			if !slices.Equal(ran, tc.wantRan) {
				t.Errorf("ran = %v, want %v", ran, tc.wantRan)
			}
			var gotSkipped []string
			for id, v := range skipped {
				gotSkipped = append(gotSkipped, id)
				if v.SkipReason != health.CheckDisabled {
					t.Errorf("%s: SkipReason = %v, want CHECK_DISABLED", id, v.SkipReason)
				}
			}
			slices.Sort(gotSkipped)
			if !slices.Equal(gotSkipped, tc.wantSkipped) {
				t.Errorf("skipped = %v, want %v", gotSkipped, tc.wantSkipped)
			}

			// Signal collection reads only what runs, and the single-check path
			// agrees with the batch path.
			runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{})
			if err != nil {
				t.Fatalf("RunnableChecks: %v", err)
			}
			var gotRunnable []string
			for _, cd := range runnable {
				gotRunnable = append(gotRunnable, cd.ID)
			}
			slices.Sort(gotRunnable)
			if !slices.Equal(gotRunnable, tc.wantRan) {
				t.Errorf("RunnableChecks = %v, want %v", gotRunnable, tc.wantRan)
			}
			for _, id := range []string{"stable", "experimental_a", "experimental_b"} {
				v, err := cat.Evaluate(id, twoSignalMeasurement{First: 1})
				if err != nil {
					t.Fatalf("Evaluate(%s): %v", id, err)
				}
				if want := !slices.Contains(tc.wantRan, id); v.Skipped != want {
					t.Errorf("Evaluate(%s).Skipped = %v, want %v", id, v.Skipped, want)
				}
			}
		})
	}
}

// The skip says why, so an operator can tell a default-off experimental check
// from one they disabled themselves.
func TestExperimentalChecks_SkipDetailNamesTheCause(t *testing.T) {
	cat := expCatalog(expDefs(), catalog.WithDisabledChecks("experimental_b"))
	_, skipped := expOutcome(t, cat)
	if got := skipped["experimental_a"].SkipDetail; !strings.Contains(got, "experimental") {
		t.Errorf("default-off detail = %q, want it to say the check is experimental", got)
	}
	if got := skipped["experimental_b"].SkipDetail; strings.Contains(got, "experimental") {
		t.Errorf("operator-disabled detail = %q, want the operator disable, not the experimental gate", got)
	}
}

// Enabling lifts only the experimental gate. A check that does not apply to
// this node still skips for the reason that is true.
func TestExperimentalChecks_EnablingDoesNotOverrideOtherSkips(t *testing.T) {
	defs := []health.CheckDefinition{expFixture("experimental_elsewhere", health.Experimental, "another_arch")}
	cat := expCatalog(defs, catalog.WithAllExperimentalChecks())
	ran, skipped := expOutcome(t, cat)
	if len(ran) != 0 {
		t.Fatalf("ran = %v, want none", ran)
	}
	if got := skipped["experimental_elsewhere"].SkipReason; got != health.ArchitectureUnsupported {
		t.Errorf("SkipReason = %v, want SIGNAL_UNAVAILABLE_ARCHITECTURE", got)
	}
}

// The zero value is Stable, so a check that never mentions maturity runs
// without being enabled.
func TestExperimentalChecks_UnsetMaturityIsStable(t *testing.T) {
	cd := expFixture("unset", health.Stable)
	cd.MaturityLevel = 0
	ran, _ := expOutcome(t, expCatalog([]health.CheckDefinition{cd}))
	if !slices.Equal(ran, []string{"unset"}) {
		t.Errorf("ran = %v, want the check to run", ran)
	}
}
