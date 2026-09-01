package health

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNodeVerdict_ExitCode(t *testing.T) {
	tests := []struct {
		severity Severity
		want     int
	}{
		{Pass, 0},
		{Info, 0},
		{Warning, 3},
		{Critical, 1},
		{Unknown, 2},
	}
	for _, tt := range tests {
		nv := &NodeVerdict{OverallSeverity: tt.severity}
		if got := nv.ExitCode(); got != tt.want {
			t.Errorf("ExitCode() for %v = %d, want %d", tt.severity, got, tt.want)
		}
	}
}

func TestAggregateNodeVerdict_SchemaVersion(t *testing.T) {
	if got, want := NodeVerdictSchemaVersion, "0.1"; got != want {
		t.Errorf("NodeVerdictSchemaVersion = %q, want %q", got, want)
	}

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
	if nv.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", nv.ExitCode())
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
	if nv.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", nv.ExitCode())
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
	if nv.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", nv.ExitCode())
	}
	if !nv.RecommendedAction.IsZero() {
		t.Errorf("recommended action = %+v, want NoAction", nv.RecommendedAction)
	}
}

func TestAggregateNodeVerdict_RecommendedAction(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		verdicts []CheckVerdict
		want     RecommendedAction
	}{
		{
			name: "single top-severity check surfaces its action",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Pass, Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionResetGPU), Timestamp: now},
			},
			want: Drain(DiagnosticActionResetGPU),
		},
		{
			name: "drain outranks monitor at the same top severity",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Monitor, Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionReboot), Timestamp: now},
			},
			want: Drain(DiagnosticActionReboot),
		},
		{
			name: "severity gates: more-severe monitor beats less-severe drain",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Monitor, Timestamp: now},
				{CheckID: "b", Severity: Warning, RecommendedAction: Drain(DiagnosticActionReboot), Timestamp: now},
			},
			want: Monitor,
		},
		{
			name: "two top-severity drains broken by priority list",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Drain(DiagnosticActionReboot), Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionContactSupport), Timestamp: now},
			},
			want: Drain(DiagnosticActionContactSupport),
		},
		{
			name: "equal-rank drains keep the first encountered",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Drain(DiagnosticActionResetGPU), Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionResetGPU), Timestamp: now},
			},
			want: Drain(DiagnosticActionResetGPU),
		},
		{
			// check_collection outranks retry_collection regardless of
			// CheckResults order: a concrete collection failure is more
			// actionable than a timed-out read. retry_collection is listed
			// first to catch the old equal-rank "first encountered wins".
			name: "collection failure outranks timeout regardless of order",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Drain(DiagnosticActionRetryCollection), Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionCheckCollection), Timestamp: now},
			},
			want: Drain(DiagnosticActionCheckCollection),
		},
		{
			// An observed fault outranks "could not collect": collection
			// actions rank below the hardware-remediation actions.
			name: "observed fault outranks collection failure",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Critical, RecommendedAction: Drain(DiagnosticActionCheckCollection), Timestamp: now},
				{CheckID: "b", Severity: Critical, RecommendedAction: Drain(DiagnosticActionResetGPU), Timestamp: now},
			},
			want: Drain(DiagnosticActionResetGPU),
		},
		{
			name: "all pass yields no action",
			verdicts: []CheckVerdict{
				{CheckID: "a", Severity: Pass, Timestamp: now},
			},
			want: NoAction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nv := AggregateNodeVerdict(tt.verdicts)
			if nv.RecommendedAction != tt.want {
				t.Errorf("recommended action = %+v, want %+v", nv.RecommendedAction, tt.want)
			}
		})
	}
}

// TestAggregateNodeVerdict_RecommendedAction_MostlySkipped guards that
// the one check that did complete still drives both severity and the
// surfaced action, however many of its peers were skipped.
func TestAggregateNodeVerdict_RecommendedAction_MostlySkipped(t *testing.T) {
	now := time.Now()
	verdicts := []CheckVerdict{
		{CheckID: "a", Severity: Critical, RecommendedAction: Drain(DiagnosticActionContactSupport), Timestamp: now},
		{CheckID: "b", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
		{CheckID: "c", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
		{CheckID: "d", Skipped: true, SkipReason: ArchitectureUnsupported, Timestamp: now},
	}

	nv := AggregateNodeVerdict(verdicts)

	if nv.OverallSeverity != Critical {
		t.Fatalf("overall severity = %v, want CRITICAL", nv.OverallSeverity)
	}
	if want := Drain(DiagnosticActionContactSupport); nv.RecommendedAction != want {
		t.Errorf("recommended action = %+v, want %+v", nv.RecommendedAction, want)
	}
}

func TestDiagnosticActionPriorityRank(t *testing.T) {
	// contact_support outranks reboot; an action absent from the priority
	// list ranks last (Unspecified is not listed).
	if DiagnosticActionContactSupport.priorityRank() >= DiagnosticActionReboot.priorityRank() {
		t.Errorf("contact_support rank %d should be < reboot rank %d",
			DiagnosticActionContactSupport.priorityRank(), DiagnosticActionReboot.priorityRank())
	}
	if got, want := DiagnosticActionUnspecified.priorityRank(), len(diagnosticActionPriority); got != want {
		t.Errorf("unlisted action rank = %d, want %d (last)", got, want)
	}
	// Both collection actions are listed, with check_collection (a concrete
	// failure) ahead of retry_collection (a timeout).
	if DiagnosticActionCheckCollection.priorityRank() >= DiagnosticActionRetryCollection.priorityRank() {
		t.Errorf("check_collection rank %d should be < retry_collection rank %d",
			DiagnosticActionCheckCollection.priorityRank(), DiagnosticActionRetryCollection.priorityRank())
	}
	if got := DiagnosticActionRetryCollection.priorityRank(); got >= len(diagnosticActionPriority) {
		t.Errorf("retry_collection should be listed, got rank %d (>= %d)", got, len(diagnosticActionPriority))
	}
}
