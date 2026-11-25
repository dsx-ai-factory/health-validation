// SPDX-FileCopyrightText: 2026 Tejesh Vaish
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

type redAssessment struct {
	AssessmentAvailable bool `json:"assessment_available"`
}

func (redAssessment) Category() health.Category { return health.Node }

func (redAssessment) Errors() []health.ErroredMeasurement { return nil }

type redRecorder struct {
	calls int
}

func (r *redRecorder) next() { r.calls++ }

func unsafeSeverityOnly(verdict health.NodeVerdict, next func()) {
	if verdict.OverallSeverity == health.Pass {
		next()
	}
}

func redMissingCPU(t *testing.T) health.NodeVerdict {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("synthetic"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{cpu.CPUCount(), {
		ID: "synthetic_assessment", Category: health.Node,
		Provenance: health.Private, MaturityLevel: health.Experimental,
		ConditionScope: health.NodeScope, ExecutionClass: health.Passive,
		SchedulingCadence: health.Continuous,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		RequiredSignals:   []string{"assessment_available"},
		Rules:             []health.Rule{{Expression: "assessment_available == false", Severity: health.Unknown, Message: "Synthetic assessment unavailable."}},
	}} {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := cat.EvaluateAll(redAssessment{AssessmentAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func assertRedMissingCPU(t *testing.T, verdict health.NodeVerdict) {
	t.Helper()
	if verdict.OverallSeverity != health.Pass || len(verdict.CheckResults) != 1 || len(verdict.SkippedChecks) != 1 {
		t.Fatalf("unexpected real producer output: %+v", verdict)
	}
	if verdict.CheckResults[0].CheckID != "synthetic_assessment" || verdict.CheckResults[0].Severity != health.Pass {
		t.Fatalf("unexpected result: %+v", verdict.CheckResults[0])
	}
	if verdict.SkippedChecks[0].CheckID != "cpu_count" || verdict.SkippedChecks[0].SkipReason != health.SensorMissing {
		t.Fatalf("unexpected skip: %+v", verdict.SkippedChecks[0])
	}
	t.Logf("real catalog: overall=%s result=%s/%s skip=%s/%s", verdict.OverallSeverity, verdict.CheckResults[0].CheckID, verdict.CheckResults[0].Severity, verdict.SkippedChecks[0].CheckID, verdict.SkippedChecks[0].SkipReason)
}

func TestCallerDoesNotContinueWithoutCPUAssessment(t *testing.T) {
	verdict := redMissingCPU(t)
	assertRedMissingCPU(t, verdict)
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	decision, err := consume(verdict, &output, recorder.next)
	if err != nil || decision.proceed {
		t.Fatalf("missing CPU decision = %+v, error = %v", decision, err)
	}
	if recorder.calls != 0 {
		t.Fatalf("continuation calls = %d, want 0 when CPU assessment is missing", recorder.calls)
	}
}

type continuationRecorder struct {
	output *bytes.Buffer
	err    error
	calls  int
}

func (r *continuationRecorder) next() error {
	r.calls++
	if _, err := decodeDocument(r.output.Bytes()); err != nil {
		return fmt.Errorf("continuation ran before complete JSON: %w", err)
	}
	return r.err
}

func decodeDocument(data []byte) (health.NodeVerdict, error) {
	var verdict health.NodeVerdict
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&verdict); err != nil {
		return verdict, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return verdict, fmt.Errorf("expected EOF after one verdict, got %v", err)
	}
	return verdict, nil
}

func TestUnsafeReferenceDiscriminatesMissingCPU(t *testing.T) {
	verdict := redMissingCPU(t)
	assertRedMissingCPU(t, verdict)
	var unsafe redRecorder
	unsafeSeverityOnly(verdict, unsafe.next)
	if unsafe.calls != 1 {
		t.Fatalf("unsafe control calls = %d, want 1", unsafe.calls)
	}
	assertConsumption(t, verdict, false, "coverage")
	t.Log("same actual missing-CPU verdict: unsafe calls=1; real caller calls=0")
}

func assertConsumption(t *testing.T, verdict health.NodeVerdict, proceed bool, reason string) {
	t.Helper()
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	decision, err := consume(verdict, &output, recorder.next)
	if err != nil || decision.proceed != proceed || decision.reason != reason {
		t.Fatalf("consume: decision=%+v error=%v, want proceed=%v reason=%s", decision, err, proceed, reason)
	}
	wantCalls := 0
	if proceed {
		wantCalls = 1
	}
	if recorder.calls != wantCalls {
		t.Fatalf("calls=%d, want %d", recorder.calls, wantCalls)
	}
	assertJSONContract(t, output.Bytes())
}

func passingVerdict(t *testing.T) health.NodeVerdict {
	t.Helper()
	fixture, err := selectScenario("pass")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := newCatalog(fixture.disabled)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := evaluate(cat, fixture.measurements)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func TestWiring(t *testing.T) {
	cat, err := newCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	measurements := []health.Measurement{cpu.CPUMeasurement{}, assessment{}}
	if err := catalog.CheckSignalUniqueness(measurements...); err != nil {
		t.Fatal(err)
	}
	if err := cat.CheckCoverage(measurements...); err != nil {
		t.Fatal(err)
	}
	check := syntheticAssessment()
	if check.Provenance != health.Private || check.MaturityLevel != health.Experimental {
		t.Fatalf("synthetic check provenance/maturity = %v/%v", check.Provenance, check.MaturityLevel)
	}
	if cat.Len() != 2 {
		t.Fatalf("registered checks=%d, want 2", cat.Len())
	}
}

type scenarioExpectation struct {
	name       string
	overall    health.Severity
	cpu        health.Severity
	assessment health.Severity
	skip       health.SkipReason
	results    int
	skips      int
	status     int
	calls      int
}

func TestSevenScenarios(t *testing.T) {
	cases := []scenarioExpectation{
		{name: "pass", overall: health.Pass, cpu: health.Pass, assessment: health.Pass, results: 2, status: 0, calls: 1},
		{name: "critical", overall: health.Critical, cpu: health.Critical, assessment: health.Pass, results: 2, status: 1},
		{name: "unknown", overall: health.Unknown, cpu: health.Pass, assessment: health.Unknown, results: 2, status: 1},
		{name: "missing", overall: health.Pass, assessment: health.Pass, results: 1, skips: 1, skip: health.SensorMissing, status: 1},
		{name: "disabled", overall: health.Pass, assessment: health.Pass, results: 1, skips: 1, skip: health.CheckDisabled, status: 1},
		{name: "collection-error", overall: health.Critical, cpu: health.Critical, assessment: health.Pass, results: 2, status: 1},
		{name: "empty", overall: health.Pass, skips: 2, skip: health.SensorMissing, status: 1},
	}
	for _, tc := range cases {
		assertScenario(t, tc)
		assertRun(t, tc)
	}
}

func assertScenario(t *testing.T, tc scenarioExpectation) {
	t.Helper()
	fixture, err := selectScenario(tc.name)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := newCatalog(fixture.disabled)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	decision, err := evaluateAndConsume(cat, fixture.measurements, &output, recorder.next)
	if err != nil || decision.proceed != (tc.calls == 1) || recorder.calls != tc.calls {
		t.Fatalf("%s: decision=%+v calls=%d error=%v", tc.name, decision, recorder.calls, err)
	}
	verdict := assertJSONContract(t, output.Bytes())
	assertScenarioVerdict(t, tc, verdict)
	t.Logf("%s: overall=%s results=%d skips=%d calls=%d", tc.name, verdict.OverallSeverity, len(verdict.CheckResults), len(verdict.SkippedChecks), recorder.calls)
}

func assertScenarioVerdict(t *testing.T, tc scenarioExpectation, verdict health.NodeVerdict) {
	t.Helper()
	if verdict.OverallSeverity != tc.overall || len(verdict.CheckResults) != tc.results || len(verdict.SkippedChecks) != tc.skips {
		t.Fatalf("%s: unexpected verdict %+v", tc.name, verdict)
	}
	if verdict.NodeID != "synthetic-node" {
		t.Fatalf("node_id=%q", verdict.NodeID)
	}
	expected := map[string]health.Severity{"cpu_count": tc.cpu, "synthetic_assessment": tc.assessment}
	for _, result := range verdict.CheckResults {
		if want, ok := expected[result.CheckID]; !ok || result.Severity != want {
			t.Fatalf("%s: unexpected result %+v", tc.name, result)
		}
	}
	assertScenarioSkips(t, tc, verdict.SkippedChecks)
	assertScenarioDetails(t, tc.name, verdict)
}

func assertScenarioSkips(t *testing.T, tc scenarioExpectation, skips []health.CheckVerdict) {
	t.Helper()
	for _, skip := range skips {
		if !skip.Skipped || skip.SkipReason != tc.skip {
			t.Fatalf("%s: unexpected skip %+v", tc.name, skip)
		}
	}
}

func assertScenarioDetails(t *testing.T, name string, verdict health.NodeVerdict) {
	t.Helper()
	if name == "missing" || name == "disabled" {
		if verdict.SkippedChecks[0].CheckID != "cpu_count" {
			t.Fatalf("unexpected skipped check %+v", verdict.SkippedChecks[0])
		}
	}
	if name == "collection-error" {
		result := findResult(t, verdict, "cpu_count")
		if !slices.Contains(result.RecommendedAction.RemediationActions, health.RemediationActionCheckCollection) || len(result.Messages) == 0 {
			t.Fatalf("collection error lost recommendation/message: %+v", result)
		}
	}
	if name == "unknown" {
		result := findResult(t, verdict, "synthetic_assessment")
		if !slices.Contains(result.Messages, "Synthetic assessment unavailable.") {
			t.Fatalf("UNKNOWN was not the synthetic rule: %+v", result)
		}
	}
}

func findResult(t *testing.T, verdict health.NodeVerdict, id string) health.CheckVerdict {
	t.Helper()
	for _, result := range verdict.CheckResults {
		if result.CheckID == id {
			return result
		}
	}
	t.Fatalf("missing result %s", id)
	return health.CheckVerdict{}
}

func assertRun(t *testing.T, tc scenarioExpectation) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-scenario", tc.name}, &stdout, &stderr)
	if code != tc.status {
		t.Fatalf("%s exit=%d, want %d: %s", tc.name, code, tc.status, stderr.String())
	}
	if strings.Count(stderr.String(), "continued synthetic workload") != tc.calls {
		t.Fatalf("%s: continuation marker: %q", tc.name, stderr.String())
	}
	assertScenarioVerdict(t, tc, assertJSONContract(t, stdout.Bytes()))
}

func assertJSONContract(t *testing.T, data []byte) health.NodeVerdict {
	t.Helper()
	verdict, err := decodeDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	assertRequiredKeys(t, raw, "schema_version", "catalog_version", "timestamp", "trace_id", "severity_by_domain", "check_results", "overall_severity")
	if verdict.SchemaVersion != "0.4" || verdict.CatalogVersion == "" || verdict.Timestamp.IsZero() || verdict.TraceID == "" {
		t.Fatalf("invalid output metadata: %+v", verdict)
	}
	assertWireSeverity(t, raw["overall_severity"])
	for _, forbidden := range []string{"decision", "coverage", "catalog_commit"} {
		if _, exists := raw[forbidden]; exists {
			t.Fatalf("unexpected output field %s", forbidden)
		}
	}
	assertCheckRecords(t, verdict, raw)
	return verdict
}

func assertRequiredKeys(t *testing.T, raw map[string]json.RawMessage, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, exists := raw[key]; !exists {
			t.Fatalf("required JSON key %s absent", key)
		}
	}
}

func assertWireSeverity(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if !slices.Contains([]string{`"PASS"`, `"INFO"`, `"WARNING"`, `"CRITICAL"`, `"UNKNOWN"`}, string(raw)) {
		t.Fatalf("severity is not a supported wire string: %s", raw)
	}
}

func assertCheckRecords(t *testing.T, verdict health.NodeVerdict, raw map[string]json.RawMessage) {
	t.Helper()
	if len(verdict.CheckResults) == 0 && string(raw["check_results"]) != "null" {
		t.Fatalf("no assessed checks must preserve null: %s", raw["check_results"])
	}
	if len(verdict.SkippedChecks) == 0 {
		if _, exists := raw["skipped_checks"]; exists {
			t.Fatal("empty skipped_checks must be omitted")
		}
	}
	spans := make(map[string]bool)
	for _, field := range []string{"check_results", "skipped_checks"} {
		var records []map[string]json.RawMessage
		if data, exists := raw[field]; exists {
			if err := json.Unmarshal(data, &records); err != nil {
				t.Fatal(err)
			}
		}
		for _, record := range records {
			assertCheckRecord(t, record, verdict.TraceID, spans)
		}
	}
}

func assertCheckRecord(t *testing.T, raw map[string]json.RawMessage, trace string, spans map[string]bool) {
	t.Helper()
	assertRequiredKeys(t, raw, "timestamp", "check_id", "span_id", "trace_id", "severity", "category", "condition_scope", "maturity_level", "skipped", "nvidia_endorsed")
	assertWireSeverity(t, raw["severity"])
	var result health.CheckVerdict
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Timestamp.IsZero() || result.TraceID != trace || result.SpanID == "" || spans[result.SpanID] {
		t.Fatalf("invalid check identity/time: %+v", result)
	}
	spans[result.SpanID] = true
	if string(raw["condition_scope"]) != `"NODE"` {
		t.Fatalf("unexpected scope: %s", raw["condition_scope"])
	}
	assertCheckProvenance(t, raw, result)
	if result.Skipped {
		assertRequiredKeys(t, raw, "skip_reason", "skip_detail")
		if !slices.Contains([]string{`"SENSOR_MISSING"`, `"CHECK_DISABLED"`}, string(raw["skip_reason"])) {
			t.Fatalf("unexpected skip reason %s", raw["skip_reason"])
		}
	}
}

func assertCheckProvenance(t *testing.T, raw map[string]json.RawMessage, result health.CheckVerdict) {
	t.Helper()
	switch result.CheckID {
	case "cpu_count":
		if string(raw["category"]) != `"cpu"` || !result.NVIDIAEndorsed || string(raw["maturity_level"]) != `"stable"` {
			t.Fatalf("CPU metadata changed: %+v", result)
		}
	case "synthetic_assessment":
		if string(raw["category"]) != `"node"` || result.NVIDIAEndorsed || string(raw["maturity_level"]) != `"experimental"` {
			t.Fatalf("synthetic metadata changed: %+v", result)
		}
	default:
		t.Fatalf("unexpected check ID %s", result.CheckID)
	}
}

func assertInvalidContract(t *testing.T, verdict health.NodeVerdict) {
	t.Helper()
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	_, err := consume(verdict, &output, recorder.next)
	if err == nil || recorder.calls != 0 || output.Len() != 0 {
		t.Fatalf("invalid contract: error=%v calls=%d output=%q", err, recorder.calls, output.String())
	}
}

func TestSchemaAndCatalogVersionBoundary(t *testing.T) {
	for _, schema := range []string{"", "0.3", "0.5", "1.0"} {
		verdict := passingVerdict(t)
		verdict.SchemaVersion = schema
		assertInvalidContract(t, verdict)
	}
	verdict := passingVerdict(t)
	verdict.CatalogVersion = ""
	assertInvalidContract(t, verdict)
	t.Log("real PASS verdict with missing/unsupported schema or empty catalog version: rejected before output and continuation")
}

func TestInvalidNumericSeverities(t *testing.T) {
	for _, invalid := range []health.Severity{-1, 99} {
		for _, location := range []string{"overall", "result", "skip", "domain", "component"} {
			verdict := passingVerdict(t)
			switch location {
			case "overall":
				verdict.OverallSeverity = invalid
			case "result":
				verdict.CheckResults[0].Severity = invalid
			case "skip":
				verdict.SkippedChecks = []health.CheckVerdict{{Severity: invalid}}
			case "domain":
				verdict.SeverityByDomain[health.CPU] = invalid
			case "component":
				verdict.AffectedComponents = []health.AffectedComponent{{MaxSeverity: invalid}}
			}
			assertInvalidContract(t, verdict)
		}
	}
}

func coverageMutation(verdict health.NodeVerdict, name string) health.NodeVerdict {
	switch name {
	case "missing":
		verdict.CheckResults = verdict.CheckResults[:1]
	case "duplicate":
		verdict.CheckResults[1] = verdict.CheckResults[0]
	case "unexpected":
		verdict.CheckResults[0].CheckID = "other"
	case "category":
		verdict.CheckResults[0].Category = health.GPU
	case "hidden-skip":
		verdict.CheckResults[0].Skipped = true
	case "skip-reason":
		verdict.CheckResults[0].SkipReason = health.SensorMissing
	case "no-results":
		verdict.CheckResults = nil
	case "extra-skip":
		verdict.SkippedChecks = []health.CheckVerdict{{Skipped: true, CheckID: "cpu_count", Category: health.CPU, SkipReason: health.CheckDisabled}}
	}
	return verdict
}

func TestRequiredCoverageBoundary(t *testing.T) {
	for _, name := range []string{"missing", "duplicate", "unexpected", "category", "hidden-skip", "skip-reason", "no-results", "extra-skip"} {
		verdict := coverageMutation(passingVerdict(t), name)
		var output bytes.Buffer
		recorder := continuationRecorder{output: &output}
		decision, err := consume(verdict, &output, recorder.next)
		if err != nil || decision.proceed || decision.reason != "coverage" || recorder.calls != 0 {
			t.Fatalf("%s: decision=%+v calls=%d error=%v", name, decision, recorder.calls, err)
		}
		if _, err := decodeDocument(output.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmptyAndNarrowedRegistry(t *testing.T) {
	for _, registerCPU := range []bool{false, true} {
		cat := catalog.NewEmpty(catalog.WithArchitecture("synthetic"), catalog.WithExecutionContext(health.OperatorInfra))
		if registerCPU {
			if err := cat.Register(cpu.CPUCount()); err != nil {
				t.Fatal(err)
			}
		}
		verdict, err := evaluate(cat, []health.Measurement{cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 8}})
		if err != nil || verdict.OverallSeverity != health.Pass {
			t.Fatalf("producer: verdict=%+v error=%v", verdict, err)
		}
		assertConsumption(t, verdict, false, "coverage")
	}
	t.Log("actual empty and CPU-only registries: PASS producer, caller rejects fixed missing assessment")
}

func TestTypedSeverityBoundary(t *testing.T) {
	cases := []struct {
		reason   string
		severity health.Severity
	}{
		{"INFO", health.Info}, {"WARNING", health.Warning}, {"CRITICAL", health.Critical}, {"UNKNOWN", health.Unknown},
	}
	for _, tc := range cases {
		verdict := passingVerdict(t)
		verdict.CheckResults[0].Severity = tc.severity
		fixture := health.AggregateNodeVerdict(verdict.CheckResults)
		assertConsumption(t, fixture, false, tc.reason)
		fixture.OverallSeverity = health.Pass
		assertConsumption(t, fixture, false, tc.reason)
		verdict = passingVerdict(t)
		verdict.OverallSeverity = tc.severity
		assertConsumption(t, verdict, false, tc.reason)
	}
}

func TestRealUnknownOutranksCritical(t *testing.T) {
	cat, err := newCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := evaluate(cat, []health.Measurement{cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 7}, assessment{AssessmentAvailable: false}})
	if err != nil || verdict.OverallSeverity != health.Unknown {
		t.Fatalf("real mixed severity: verdict=%+v error=%v", verdict, err)
	}
	if findResult(t, verdict, "cpu_count").Severity != health.Critical || findResult(t, verdict, "synthetic_assessment").Severity != health.Unknown {
		t.Fatalf("unexpected per-check results: %+v", verdict.CheckResults)
	}
	assertConsumption(t, verdict, false, "UNKNOWN")
}

func assertEvaluationFailure(t *testing.T, cat *catalog.Catalog, measurements []health.Measurement, target error) {
	t.Helper()
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	_, err := evaluateAndConsume(cat, measurements, &output, recorder.next)
	if err == nil || output.Len() != 0 || recorder.calls != 0 {
		t.Fatalf("evaluation failure: error=%v output=%q calls=%d", err, output.String(), recorder.calls)
	}
	if target != nil && !errors.Is(err, target) {
		t.Fatalf("error=%v, want %v", err, target)
	}
	t.Logf("actual evaluation error surfaced before output/continuation: %v", err)
}

func TestActualEvaluationErrors(t *testing.T) {
	assertEvaluationFailure(t, catalog.NewEmpty(), nil, catalog.ErrArchitectureUnset)
	cat, err := newCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 8}
	assertEvaluationFailure(t, cat, []health.Measurement{duplicate, duplicate}, health.ErrAmbiguousMeasurement)
	cat = catalog.NewEmpty(catalog.WithArchitecture("synthetic"), catalog.WithExecutionContext(health.OperatorInfra))
	check := syntheticAssessment()
	check.Rules[0].Expression = "assessment_available +"
	if err := cat.Register(check); err != nil {
		t.Fatal(err)
	}
	assertEvaluationFailure(t, cat, []health.Measurement{assessment{AssessmentAvailable: true}}, nil)
}

type failingWriter struct {
	err   error
	calls int
}

func (w *failingWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, w.err
}

func TestOutputFailurePreventsContinuation(t *testing.T) {
	verdict := passingVerdict(t)
	want := errors.New("synthetic writer failure")
	writer := failingWriter{err: want}
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output}
	_, err := consume(verdict, &writer, recorder.next)
	if !errors.Is(err, want) || recorder.calls != 0 || writer.calls != 1 {
		t.Fatalf("writer failure: error=%v next=%d writes=%d", err, recorder.calls, writer.calls)
	}
	t.Log("real PASS with failing output writer: one write, zero continuation calls, error surfaced")
}

func TestContinuationErrorAfterOutputWithoutRetry(t *testing.T) {
	verdict := passingVerdict(t)
	want := errors.New("synthetic continuation failure")
	var output bytes.Buffer
	recorder := continuationRecorder{output: &output, err: want}
	_, err := consume(verdict, &output, recorder.next)
	if !errors.Is(err, want) || recorder.calls != 1 {
		t.Fatalf("continuation failure: error=%v calls=%d", err, recorder.calls)
	}
	assertJSONContract(t, output.Bytes())
	t.Log("continuation observed complete JSON+EOF, called once, returned error without retry")
}

func TestScenarioArgumentsRejectWithoutSubstitution(t *testing.T) {
	for _, args := range [][]string{nil, {"-scenario", "typo"}, {"-scenario", ""}, {"-scenario", "pass", "extra"}, {"-other"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "continued synthetic workload") || stderr.Len() == 0 {
			t.Fatalf("args=%q: exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}
