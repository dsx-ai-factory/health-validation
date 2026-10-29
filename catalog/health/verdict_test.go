// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestAggregateNodeVerdict_SchemaVersion(t *testing.T) {
	nv := AggregateNodeVerdict(nil)
	if got, want := nv.SchemaVersion, NodeVerdictSchemaVersion; got != want {
		t.Errorf("SchemaVersion = %q, want %q", got, want)
	}
}

// Every run gets one trace_id, shared by (and echoed on) all its checks;
// every check invocation — run or skipped — gets a unique span_id.
func TestAggregateNodeVerdict_RunAndTraceIDs(t *testing.T) {
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Pass},
		{CheckID: "b", Severity: Critical},
		{CheckID: "c", Skipped: true, SkipReason: CheckDisabled},
	}
	nv := AggregateNodeVerdict(verdicts)

	if nv.TraceID == "" {
		t.Fatal("NodeVerdict.TraceID is empty")
	}

	all := append(append([]CheckVerdict{}, nv.CheckResults...), nv.SkippedChecks...)
	if len(all) != 3 {
		t.Fatalf("got %d check verdicts, want 3", len(all))
	}

	seen := make(map[string]bool)
	for _, v := range all {
		if v.SpanID == "" {
			t.Errorf("check %q has empty span_id", v.CheckID)
		}
		if seen[v.SpanID] {
			t.Errorf("duplicate span_id %q on check %q", v.SpanID, v.CheckID)
		}
		seen[v.SpanID] = true
		if v.TraceID != nv.TraceID {
			t.Errorf("check %q trace_id = %q, want the run's %q", v.CheckID, v.TraceID, nv.TraceID)
		}
		if v.SpanID == nv.TraceID {
			t.Errorf("check %q: span_id must not equal trace_id", v.CheckID)
		}
	}

	// A separate run is a distinct trace.
	if other := AggregateNodeVerdict(verdicts); other.TraceID == nv.TraceID {
		t.Error("two runs produced the same trace_id")
	}
}

func TestAggregateNodeVerdict_CPUCategoryJSON(t *testing.T) {
	nv := AggregateNodeVerdict([]CheckVerdict{
		{CheckID: "cpu", Severity: Critical, Category: CPU},
	})

	data, err := json.Marshal(nv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got struct {
		SeverityByDomain   map[string]Severity `json:"severity_by_domain"`
		AffectedComponents []AffectedComponent `json:"affected_components"`
		CheckResults       []CheckVerdict      `json:"check_results"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if severity, ok := got.SeverityByDomain["cpu"]; !ok || severity != Critical {
		t.Errorf("severity_by_domain[cpu] = %v, %t; want CRITICAL, true", severity, ok)
	}
	if _, ok := got.SeverityByDomain["7"]; ok {
		t.Errorf("numeric category key present in severity_by_domain: %s", data)
	}
	if len(got.CheckResults) != 1 || got.CheckResults[0].Category != CPU {
		t.Errorf("check_results = %+v, want one cpu result", got.CheckResults)
	}
	if len(got.AffectedComponents) != 1 || got.AffectedComponents[0].Category != CPU {
		t.Errorf("affected_components = %+v, want one cpu component", got.AffectedComponents)
	}
}

func TestNodeVerdictJSON_NodeID(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		data, err := json.Marshal(NodeVerdict{NodeID: "node-123"})
		if err != nil {
			t.Fatal(err)
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if got := string(fields["node_id"]); got != `"node-123"` {
			t.Errorf("node_id = %s, want %q", got, "node-123")
		}
	})

	t.Run("empty omitted", func(t *testing.T) {
		data, err := json.Marshal(NodeVerdict{})
		if err != nil {
			t.Fatal(err)
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["node_id"]; ok {
			t.Errorf("node_id present in JSON: %s", data)
		}
	})

	t.Run("older JSON without node ID", func(t *testing.T) {
		var verdict NodeVerdict
		if err := json.Unmarshal([]byte(`{"overall_severity":"CRITICAL"}`), &verdict); err != nil {
			t.Fatal(err)
		}
		if verdict.NodeID != "" {
			t.Errorf("NodeID = %q, want empty", verdict.NodeID)
		}
		if verdict.OverallSeverity != Critical {
			t.Errorf("OverallSeverity = %v, want CRITICAL", verdict.OverallSeverity)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		want := NodeVerdict{NodeID: "node-123"}
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}

		var got NodeVerdict
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.NodeID != want.NodeID {
			t.Errorf("NodeID = %q, want %q", got.NodeID, want.NodeID)
		}
	})
}

func TestNodeVerdictJSON_CatalogVersion(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		data, err := json.Marshal(NodeVerdict{CatalogVersion: "v1.2.3"})
		if err != nil {
			t.Fatal(err)
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if got := string(fields["catalog_version"]); got != `"v1.2.3"` {
			t.Errorf("catalog_version = %s, want %q", got, "v1.2.3")
		}
	})

	t.Run("aggregated verdict", func(t *testing.T) {
		nv := AggregateNodeVerdict(nil)
		if nv.CatalogVersion == "" {
			t.Fatal("CatalogVersion is empty")
		}

		data, err := json.Marshal(nv)
		if err != nil {
			t.Fatal(err)
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if got := string(fields["catalog_version"]); got == "" || got == `""` {
			t.Errorf("catalog_version = %s, want a non-empty value", got)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		want := NodeVerdict{CatalogVersion: "v1.2.3"}
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}

		var got NodeVerdict
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.CatalogVersion != want.CatalogVersion {
			t.Errorf("CatalogVersion = %q, want %q", got.CatalogVersion, want.CatalogVersion)
		}
	})
}

// condition_scope is always emitted. NodeScope is the zero value, so an omitted
// key would be indistinguishable from a node-scoped condition.
func TestCheckVerdictJSON_ConditionScope(t *testing.T) {
	assertScope := func(v CheckVerdict, want string) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got["condition_scope"] != want {
			t.Errorf("condition_scope = %v, want %q; json = %s", got["condition_scope"], want, data)
		}
	}

	assertScope(CheckVerdict{ConditionScope: DomainScope}, "DOMAIN")
	assertScope(CheckVerdict{}, "NODE")
}

func TestAggregateNodeVerdict_Basic(t *testing.T) {
	now := time.Now()
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Pass, Category: GPU, Timestamp: now},
		{CheckID: "b", Severity: Warning, Category: GPU, Timestamp: now},
		{CheckID: "c", Severity: Critical, Category: NVLink, Timestamp: now},
	}

	nv := AggregateNodeVerdict(verdicts)

	if nv.OverallSeverity != Critical {
		t.Errorf("overall severity = %v, want CRITICAL", nv.OverallSeverity)
	}
	if len(nv.CheckResults) != 3 {
		t.Errorf("check results = %d, want 3", len(nv.CheckResults))
	}
	if nv.SeverityByDomain[GPU] != Warning {
		t.Errorf("gpu domain severity = %v, want WARNING", nv.SeverityByDomain[GPU])
	}
	if nv.SeverityByDomain[NVLink] != Critical {
		t.Errorf("nvlink domain severity = %v, want CRITICAL", nv.SeverityByDomain[NVLink])
	}
}

func TestAggregateNodeVerdict_RecommendedAction(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []CheckVerdict
		want     RecommendedAction
	}{
		{
			name: "severity before action",
			verdicts: []CheckVerdict{
				{CheckID: "warning-service", Severity: Warning, RecommendedAction: Drain(RemediationActionServiceRequired)},
				{CheckID: "critical-reset", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU, RemediationActionServiceRequired)},
			},
			want: Drain(RemediationActionResetGPU, RemediationActionServiceRequired),
		},
		{
			name: "first action priority",
			verdicts: []CheckVerdict{
				{CheckID: "retry", Severity: Critical, RecommendedAction: Drain(RemediationActionRetryCollection)},
				{CheckID: "collection", Severity: Critical, RecommendedAction: Drain(RemediationActionCheckCollection)},
				{CheckID: "app", Severity: Critical, RecommendedAction: Drain(RemediationActionRestartApp)},
				{CheckID: "reset", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU)},
				{CheckID: "update", Severity: Critical, RecommendedAction: Drain(RemediationActionUpdateSWFW)},
				{CheckID: "restart", Severity: Critical, RecommendedAction: Drain(RemediationActionRestartOS)},
				{CheckID: "service", Severity: Critical, RecommendedAction: Drain(RemediationActionServiceRequired)},
			},
			want: Drain(RemediationActionServiceRequired),
		},
		{
			name: "later step rank before check ID",
			verdicts: []CheckVerdict{
				{CheckID: "alpha", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU, RemediationActionRestartOS, RemediationActionServiceRequired)},
				{CheckID: "zeta", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU, RemediationActionServiceRequired)},
			},
			want: Drain(RemediationActionResetGPU, RemediationActionServiceRequired),
		},
		{
			name: "longer sequence outranks its prefix",
			verdicts: []CheckVerdict{
				{CheckID: "alpha", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU)},
				{CheckID: "zeta", Severity: Critical, RecommendedAction: Drain(RemediationActionResetGPU, RemediationActionServiceRequired)},
			},
			want: Drain(RemediationActionResetGPU, RemediationActionServiceRequired),
		},
		{
			name: "check ID when ranks tie",
			verdicts: []CheckVerdict{
				{CheckID: "zeta", Severity: Critical, RecommendedAction: Drain("custom_a")},
				{CheckID: "alpha", Severity: Critical, RecommendedAction: Drain("custom_b")},
			},
			want: Drain("custom_b"),
		},
		{
			name: "non-candidates ignored",
			verdicts: []CheckVerdict{
				{CheckID: "skipped", Severity: Critical, Skipped: true, RecommendedAction: Drain(RemediationActionServiceRequired)},
				{CheckID: "monitor", Severity: Critical, RecommendedAction: Monitor},
				{CheckID: "empty", Severity: Critical, RecommendedAction: Drain()},
				{CheckID: "warning", Severity: Warning, RecommendedAction: Drain(RemediationActionRestartApp)},
			},
			want: Drain(RemediationActionRestartApp),
		},
		{
			name: "no recommendation",
			verdicts: []CheckVerdict{
				{CheckID: "pass", Severity: Pass},
				{CheckID: "monitor", Severity: Info, RecommendedAction: Monitor},
			},
			want: NoAction,
		},
		{
			name: "unranked action remains eligible",
			verdicts: []CheckVerdict{
				{CheckID: "xid-154", Severity: Info, RecommendedAction: Drain(RemediationActionXid154)},
			},
			want: Drain(RemediationActionXid154),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AggregateNodeVerdict(tt.verdicts).RecommendedAction
			if !got.Equal(tt.want) {
				t.Errorf("RecommendedAction = %v, want %v", got, tt.want)
			}

			reversed := slices.Clone(tt.verdicts)
			slices.Reverse(reversed)
			gotReversed := AggregateNodeVerdict(reversed).RecommendedAction
			if !gotReversed.Equal(tt.want) {
				t.Errorf("RecommendedAction with reversed input = %v, want %v", gotReversed, tt.want)
			}
		})
	}
}

func TestAggregateNodeVerdict_RecommendedActionDoesNotAliasCheck(t *testing.T) {
	action := Drain(RemediationActionResetGPU, RemediationActionServiceRequired)
	nv := AggregateNodeVerdict([]CheckVerdict{{
		CheckID:           "check",
		Severity:          Critical,
		RecommendedAction: action,
	}})
	nv.RecommendedAction.RemediationActions[0] = RemediationActionRestartOS
	if got := action.RemediationActions[0]; got != RemediationActionResetGPU {
		t.Errorf("check action mutated to %q", got)
	}
}

func TestNodeActionRank(t *testing.T) {
	actions := []RemediationAction{
		RemediationActionServiceRequired,
		RemediationActionUpdateSWFW,
		RemediationActionRestartOS,
		RemediationActionResetGPU,
		RemediationActionRestartApp,
		RemediationActionCheckCollection,
		RemediationActionRetryCollection,
	}
	for i := 1; i < len(actions); i++ {
		if higher, lower := nodeActionRank(actions[i-1]), nodeActionRank(actions[i]); higher >= lower {
			t.Errorf("rank(%q) = %d, want less than rank(%q) = %d", actions[i-1], higher, actions[i], lower)
		}
	}
	for _, action := range []RemediationAction{RemediationActionHold, RemediationActionXid154, "custom"} {
		if got := nodeActionRank(action); got != len(nodeActionPriority) {
			t.Errorf("rank(%q) = %d, want unranked value %d", action, got, len(nodeActionPriority))
		}
	}
}

func TestAggregateNodeVerdict_WithSkipped(t *testing.T) {
	now := time.Now()
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Pass, Category: GPU, Timestamp: now},
		{CheckID: "b", Skipped: true, SkipReason: CheckDisabled, Category: GPU, Timestamp: now},
	}

	nv := AggregateNodeVerdict(verdicts)

	if nv.OverallSeverity != Pass {
		t.Errorf("overall severity = %v, want PASS (skipped excluded)", nv.OverallSeverity)
	}
	if len(nv.SkippedChecks) != 1 {
		t.Errorf("skipped checks = %d, want 1", len(nv.SkippedChecks))
	}
	if len(nv.CheckResults) != 1 {
		t.Errorf("check results = %d, want 1", len(nv.CheckResults))
	}
}

// TestAggregateNodeVerdict_MostlySkipped guards that a node whose checks
// were mostly skipped still reports the verdict of the checks that ran:
// skips are a first-class outcome (e.g. an H100 check on GB300 hardware),
// not a failure to assess.
func TestAggregateNodeVerdict_MostlySkipped(t *testing.T) {
	now := time.Now()
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Pass, Timestamp: now},
		{CheckID: "b", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
		{CheckID: "c", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
		{CheckID: "d", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
	}

	nv := AggregateNodeVerdict(verdicts)

	if nv.OverallSeverity != Pass {
		t.Errorf("overall severity = %v, want PASS", nv.OverallSeverity)
	}
	if len(nv.SkippedChecks) != 3 {
		t.Errorf("skipped checks = %d, want 3", len(nv.SkippedChecks))
	}
}

func TestAggregateNodeVerdict_AllPass(t *testing.T) {
	now := time.Now()
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Pass, Timestamp: now},
		{CheckID: "b", Severity: Pass, Timestamp: now},
	}

	nv := AggregateNodeVerdict(verdicts)

	if nv.OverallSeverity != Pass {
		t.Errorf("overall severity = %v, want PASS", nv.OverallSeverity)
	}
}
