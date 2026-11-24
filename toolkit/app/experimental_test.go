// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// portCheck is the experimental check these tests lean on.
const portCheck = "ib_port_state"

func writeExperimentalConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigExperimentalBlock(t *testing.T) {
	cfg := mustLoadConfig(t, writeExperimentalConfig(t, "experimental:\n  enabled: true\n  checks:\n    - "+portCheck+"\n"))
	if !cfg.Experimental.Enabled {
		t.Error("Experimental.Enabled = false, want true")
	}
	if want := []string{portCheck}; !slices.Equal(cfg.Experimental.Checks, want) {
		t.Errorf("Experimental.Checks = %v, want %v", cfg.Experimental.Checks, want)
	}

	// Off is the default, so an absent block enables nothing.
	cfg = mustLoadConfig(t, writeExperimentalConfig(t, "disabled_checks: []\n"))
	if cfg.Experimental.Enabled || len(cfg.Experimental.Checks) != 0 {
		t.Errorf("Experimental = %+v, want nothing enabled", cfg.Experimental)
	}
}

// A misspelled key must be an error: silently ignoring `enable: true` would
// leave an operator believing experimental checks run when they do not.
func TestLoadConfigRejectsMisspelledExperimentalKey(t *testing.T) {
	for _, contents := range []string{
		"experimental:\n  enable: true\n",
		"experimental:\n  check:\n    - " + portCheck + "\n",
		"experimentals:\n  enabled: true\n",
	} {
		if _, err := LoadConfig(writeExperimentalConfig(t, contents)); err == nil {
			t.Errorf("LoadConfig accepted %q", contents)
		}
	}
}

func TestExperimentalCheckIDs(t *testing.T) {
	cat := catalog.New()

	got, err := (&Config{Experimental: ExperimentalConfig{Checks: []string{"  " + portCheck + "  ", "gpu_count"}}}).ExperimentalCheckIDs(cat)
	if err != nil {
		t.Fatalf("ExperimentalCheckIDs: %v", err)
	}
	// A stable check is accepted: a check that graduates must not turn a
	// working config into an error.
	if want := []string{portCheck, "gpu_count"}; !slices.Equal(got, want) {
		t.Errorf("ExperimentalCheckIDs = %v, want %v", got, want)
	}

	for _, tc := range []struct {
		name, wantErr string
		checks        []string
	}{
		// Both lists can hold the typo, so the error names the one it is in.
		{"unknown check", `experimental.checks: unknown check "no_such_check"`, []string{"no_such_check"}},
		{"empty entry", "experimental.checks contains empty entry", []string{""}},
		{"whitespace-only entry", "experimental.checks contains empty entry", []string{"   "}},
		{"valid entry beside an unknown one", `experimental.checks: unknown check "no_such_check"`, []string{portCheck, "no_such_check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Config{Experimental: ExperimentalConfig{Checks: tc.checks}}).ExperimentalCheckIDs(cat)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func experimentalCustomCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "custom_experimental",
		Category:          health.Node,
		RequiredSignals:   []string{node.SignalGPUCount},
		ExecutionContexts: []health.ExecutionContext{health.TenantGuest},
		MaturityLevel:     health.Experimental,
		Rules:             []health.Rule{{Expression: "false", Severity: health.Critical, Message: "custom"}},
	}
}

func runnableIDs(t *testing.T, cat *catalog.Catalog) []string {
	t.Helper()
	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{})
	if err != nil {
		t.Fatalf("RunnableChecks: %v", err)
	}
	var ids []string
	for _, cd := range runnable {
		ids = append(ids, cd.ID)
	}
	return ids
}

// The operator's config decides which experimental checks run, including a
// caller's own: nothing experimental runs by default, and a stable check is
// never affected.
func TestBuildCatalogExperimentalChecks(t *testing.T) {
	tests := []struct {
		cfg         *Config
		name        string
		wantPort    bool
		wantCustom  bool
		wantStable  bool
		wantBuildOK bool
	}{
		{name: "no config", cfg: nil, wantStable: true, wantBuildOK: true},
		{name: "empty config", cfg: &Config{}, wantStable: true, wantBuildOK: true},
		{
			name: "all enabled", cfg: &Config{Experimental: ExperimentalConfig{Enabled: true}},
			wantPort: true, wantCustom: true, wantStable: true, wantBuildOK: true,
		},
		{
			name: "one enabled", cfg: &Config{Experimental: ExperimentalConfig{Checks: []string{portCheck}}},
			wantPort: true, wantStable: true, wantBuildOK: true,
		},
		{
			// enabled is not a switch that the list narrows: the two add up, so a
			// list beside enabled: true changes nothing.
			name: "enabled with a list is the same as enabled", cfg: &Config{Experimental: ExperimentalConfig{Enabled: true, Checks: []string{portCheck}}},
			wantPort: true, wantCustom: true, wantStable: true, wantBuildOK: true,
		},
		{
			// The list is still validated when it is redundant, so a typo is
			// caught before enabled: true is later removed.
			name: "an unknown ID is an error even with enabled", cfg: &Config{Experimental: ExperimentalConfig{Enabled: true, Checks: []string{"nope"}}},
		},
		{
			// A custom check is validated against the catalog that includes it.
			name: "a custom experimental check enabled by ID", cfg: &Config{Experimental: ExperimentalConfig{Checks: []string{"custom_experimental"}}},
			wantCustom: true, wantStable: true, wantBuildOK: true,
		},
		{
			name: "a disable beats enabling all",
			cfg: &Config{
				DisabledChecks: []string{portCheck},
				Experimental:   ExperimentalConfig{Enabled: true},
			},
			wantCustom: true, wantStable: true, wantBuildOK: true,
		},
		{
			name: "a disable beats enabling one",
			cfg: &Config{
				DisabledChecks: []string{portCheck},
				Experimental:   ExperimentalConfig{Checks: []string{portCheck}},
			},
			wantStable: true, wantBuildOK: true,
		},
		{name: "an unknown ID is an error", cfg: &Config{Experimental: ExperimentalConfig{Checks: []string{"nope"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat, _, err := buildCatalog(tc.cfg, []health.CheckDefinition{experimentalCustomCheck()}, "GB200", health.DriverVersion{}, health.TenantGuest)
			if !tc.wantBuildOK {
				if err == nil {
					t.Fatal("buildCatalog accepted the config, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildCatalog: %v", err)
			}
			got := runnableIDs(t, cat)
			for id, want := range map[string]bool{portCheck: tc.wantPort, "custom_experimental": tc.wantCustom, "gpu_count": tc.wantStable} {
				if slices.Contains(got, id) != want {
					t.Errorf("%s runnable = %v, want %v", id, !want, want)
				}
			}
		})
	}
}

// `collect` reads sensors for the checks it would run. An experimental check
// left off must not pull a sensor in, or collection would read hardware for a
// check that never evaluates.
func TestCollectionCategoriesLeaveOutExperimentalChecksThatAreOff(t *testing.T) {
	selectsOnlyPort := func(experimental ExperimentalConfig) *Config {
		return &Config{Checks: map[string]map[string]any{portCheck: {}}, Experimental: experimental}
	}

	for _, tc := range []struct {
		cfg     *Config
		name    string
		wantErr bool
	}{
		{name: "off by default", cfg: selectsOnlyPort(ExperimentalConfig{}), wantErr: true},
		{name: "enabled by ID", cfg: selectsOnlyPort(ExperimentalConfig{Checks: []string{portCheck}})},
		{name: "enabled for all", cfg: selectsOnlyPort(ExperimentalConfig{Enabled: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat, _, err := buildCatalog(tc.cfg, nil, "", health.DriverVersion{}, health.TenantGuest)
			if err != nil {
				t.Fatalf("buildCatalog: %v", err)
			}
			wanted, err := collectionCategories(cat, tc.cfg)
			if tc.wantErr {
				// The error says why the selection came to nothing, not only that it did.
				if err == nil || !strings.Contains(err.Error(), "selects no checks") || !strings.Contains(err.Error(), portCheck) {
					t.Fatalf("collectionCategories = %v, %v; want the no-checks error naming %s", wanted, err, portCheck)
				}
				return
			}
			if err != nil {
				t.Fatalf("collectionCategories: %v", err)
			}
			if !wanted[health.InfiniBand] {
				t.Errorf("wanted = %v, want InfiniBand", wanted)
			}
		})
	}

	// With nothing selected explicitly, every check that will run decides the
	// categories, and the experimental one alone is not enough to add its own.
	cfg := &Config{}
	cat, _, err := buildCatalog(cfg, nil, "", health.DriverVersion{}, health.TenantGuest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectionCategories(cat, cfg); err != nil {
		t.Errorf("collectionCategories with the default selection: %v", err)
	}
}

// End to end through the CLI: by default the experimental check is reported as
// skipped, with a detail that says why, and the node verdict is not affected.
// Enabling it, for one check or all, makes it run.
func TestRunExperimentalCheckOffUnlessConfigured(t *testing.T) {
	find := func(results []health.CheckVerdict) *health.CheckVerdict {
		for i := range results {
			if results[i].CheckID == portCheck {
				return &results[i]
			}
		}
		return nil
	}

	t.Run("default", func(t *testing.T) {
		v, _ := runFixture(t, healthyGB200())
		if find(v.CheckResults) != nil {
			t.Fatalf("%s ran by default", portCheck)
		}
		skipped := find(v.SkippedChecks)
		if skipped == nil {
			t.Fatalf("%s is in neither results nor skipped_checks", portCheck)
		}
		if skipped.SkipReason != health.CheckDisabled || !strings.Contains(skipped.SkipDetail, "experimental") {
			t.Errorf("skip = %v %q, want CHECK_DISABLED naming the experimental gate", skipped.SkipReason, skipped.SkipDetail)
		}
	})

	for name, experimental := range map[string]string{
		"enabled for all":   "experimental:\n  enabled: true\n",
		"enabled by its ID": "experimental:\n  checks:\n    - " + portCheck + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := healthyGB200()
			f.config = experimental
			v, _ := runFixture(t, f)
			if find(v.CheckResults) == nil {
				t.Fatalf("%s did not run: skipped = %+v", portCheck, find(v.SkippedChecks))
			}
			if find(v.SkippedChecks) != nil {
				t.Errorf("%s is both a result and a skip", portCheck)
			}
		})
	}

	t.Run("a disable still wins", func(t *testing.T) {
		f := healthyGB200()
		f.config = "disabled_checks:\n  - " + portCheck + "\nexperimental:\n  enabled: true\n"
		v, _ := runFixture(t, f)
		skipped := find(v.SkippedChecks)
		if skipped == nil || strings.Contains(skipped.SkipDetail, "experimental") {
			t.Errorf("skip = %+v, want the operator disable", skipped)
		}
	})
}

// countingIBSensor is the healthy port sensor, counting how often it is read.
type countingIBSensor struct{ calls *int }

func (countingIBSensor) ID() string               { return "test.infiniband" }
func (countingIBSensor) Signals() collect.Signals { return healthyIBSensor{}.Signals() }

func (s countingIBSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	*s.calls++
	return healthyIBSensor{}.Measure(ctx, opts...)
}

// A check that does not run must not cost a collection: the port sensor is
// read only when the config enables the check that needs it.
func TestExperimentalCheckOffReadsNothing(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		wantReads    int
	}{
		{name: "default", wantReads: 0},
		{name: "enabled by ID", config: "experimental:\n  checks:\n    - " + portCheck + "\n", wantReads: 1},
		{name: "enabled for all", config: "experimental:\n  enabled: true\n", wantReads: 1},
		{name: "enabled but disabled", config: "disabled_checks:\n  - " + portCheck + "\nexperimental:\n  enabled: true\n", wantReads: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			a := New().WithProgramName("test")
			a.host = fakeHost{b: spyBackend{called: new(bool)}}
			withCleanXid(withCleanLinkFlap(withCPU(a, healthyCPU())))
			a.WithCollect(countingIBSensor{calls: &reads}, healthyIBSensor{}.Signals().IDs()...)

			args := []string{"--node-id", testNodeID}
			if tc.config != "" {
				args = append(args, "--config", writeExperimentalConfig(t, tc.config))
			}
			var stdout, stderr bytes.Buffer
			if code := a.runCommand(args, &stdout, &stderr); code == exitError {
				t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
			}
			if reads != tc.wantReads {
				t.Errorf("port sensor read %d times, want %d", reads, tc.wantReads)
			}
		})
	}
}

// The catalog gate and the config-side copy the `collect` command uses must
// agree, or `collect` would read hardware for a check that never evaluates, or
// skip one that does.
func TestExperimentalOffAgreesWithTheCatalog(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"default":     {},
		"all":         {Experimental: ExperimentalConfig{Enabled: true}},
		"one":         {Experimental: ExperimentalConfig{Checks: []string{portCheck}}},
		"custom":      {Experimental: ExperimentalConfig{Checks: []string{"custom_experimental"}}},
		"padded list": {Experimental: ExperimentalConfig{Checks: []string{"  " + portCheck + "  "}}},
		"all and one": {Experimental: ExperimentalConfig{Enabled: true, Checks: []string{portCheck}}},
	} {
		t.Run(name, func(t *testing.T) {
			cat, _, err := buildCatalog(cfg, []health.CheckDefinition{experimentalCustomCheck()}, "GB200", health.DriverVersion{}, health.TenantGuest)
			if err != nil {
				t.Fatalf("buildCatalog: %v", err)
			}
			runnable := runnableIDs(t, cat)
			checked := 0
			for _, cd := range cat.List(catalog.Filter{}) {
				if cd.MaturityLevel != health.Experimental {
					continue
				}
				checked++
				if runs, off := slices.Contains(runnable, cd.ID), cfg.experimentalOff(cd); runs == off {
					t.Errorf("%s: catalog runs it = %v but config says it is off = %v", cd.ID, runs, off)
				}
			}
			if checked < 2 {
				t.Fatalf("compared %d experimental checks, want the port check and the custom one", checked)
			}
		})
	}
}
