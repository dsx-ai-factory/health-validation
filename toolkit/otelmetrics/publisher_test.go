package otelmetrics_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit/otelmetrics"
)

// capture is a fake OTLP/HTTP collector. It records the path and the decoded
// export request of every push it receives.
type capture struct {
	paths    []string
	headers  []http.Header
	requests []*collectormetrics.ExportMetricsServiceRequest
}

func newCollector(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(r)
		if err != nil {
			t.Errorf("read export body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		req := &collectormetrics.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(body, req); err != nil {
			t.Errorf("unmarshal export request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cap.paths = append(cap.paths, r.URL.Path)
		cap.headers = append(cap.headers, r.Header.Clone())
		cap.requests = append(cap.requests, req)

		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		out, _ := proto.Marshal(&collectormetrics.ExportMetricsServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	}
	return io.ReadAll(r.Body)
}

// metricsByName flattens every scope metric in the request into a name-keyed
// map. Names are unique within a run, so a map loses nothing.
func (c *capture) metricsByName(t *testing.T) map[string]*metricspb.Metric {
	t.Helper()
	out := map[string]*metricspb.Metric{}
	for _, req := range c.requests {
		for _, rm := range req.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					out[m.GetName()] = m
				}
			}
		}
	}
	return out
}

func (c *capture) resourceAttrs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, req := range c.requests {
		for _, rm := range req.GetResourceMetrics() {
			for _, kv := range rm.GetResource().GetAttributes() {
				out[kv.GetKey()] = kv.GetValue().GetStringValue()
			}
		}
	}
	return out
}

func attrs(kvs []*commonpb.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		switch v := kv.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_StringValue:
			out[kv.GetKey()] = v.StringValue
		case *commonpb.AnyValue_BoolValue:
			if v.BoolValue {
				out[kv.GetKey()] = "true"
			} else {
				out[kv.GetKey()] = "false"
			}
		}
	}
	return out
}

// verdict is a hand-built NodeVerdict exercising every instrument: a
// non-passing check with affected entities, a passing check, and a skip.
func verdict() health.NodeVerdict {
	now := time.Now()
	return health.NodeVerdict{
		SchemaVersion:   health.NodeVerdictSchemaVersion,
		CatalogVersion:  "v0.2.0",
		Timestamp:       now,
		NodeID:          "gpu-node-1",
		TraceID:         "trace-abc",
		OverallSeverity: health.Critical,
		SeverityByDomain: map[health.Category]health.Severity{
			health.GPU:  health.Critical,
			health.Node: health.Pass,
		},
		CheckResults: []health.CheckVerdict{
			{
				Timestamp:        now,
				CheckID:          "gpu_ecc_errors",
				Category:         health.GPU,
				ConditionScope:   health.NodeScope,
				MaturityLevel:    health.Stable,
				Severity:         health.Critical,
				NVIDIAEndorsed:   true,
				AffectedEntities: []string{"GPU-0", "GPU-3"},
			},
			{
				Timestamp:      now,
				CheckID:        "gpu_count",
				Category:       health.Node,
				ConditionScope: health.NodeScope,
				MaturityLevel:  health.Stable,
				Severity:       health.Pass,
			},
		},
		SkippedChecks: []health.CheckVerdict{
			{
				Timestamp:  now,
				CheckID:    "nvlink_active",
				Category:   health.NVLink,
				Skipped:    true,
				SkipReason: health.SensorMissing,
				Severity:   health.Pass,
			},
		},
	}
}

func publish(t *testing.T, endpoint string, opts otelmetrics.Options) error {
	t.Helper()
	ctx := context.Background()
	opts.Endpoint = endpoint
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	p, err := otelmetrics.New(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if err := p.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()
	return p.Publish(ctx, verdict())
}

// TestPublishAppendsMetricsPath guards the endpoint convention: operators
// configure a base URL, and the OTLP metrics path is this package's to add.
func TestPublishAppendsMetricsPath(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(cap.paths) != 1 {
		t.Fatalf("got %d exports, want 1", len(cap.paths))
	}
	if cap.paths[0] != "/v1/metrics" {
		t.Errorf("path = %q, want /v1/metrics", cap.paths[0])
	}
}

// TestPublishHonorsExplicitPath is the other half: a collector behind a path
// prefix must not have /v1/metrics grafted onto it.
func TestPublishHonorsExplicitPath(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL+"/otlp/v1/metrics", otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if cap.paths[0] != "/otlp/v1/metrics" {
		t.Errorf("path = %q, want /otlp/v1/metrics", cap.paths[0])
	}
}

// TestPublishEmitsEveryInstrument pins the published metric set. A rename or a
// dropped instrument is a downstream break, so the names are asserted exactly.
func TestPublishEmitsEveryInstrument(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	metrics := cap.metricsByName(t)
	want := []string{
		"health_validation.node.severity",
		"health_validation.domain.severity",
		"health_validation.check.severity",
		"health_validation.check.skipped",
		"health_validation.check.affected_entities",
		"health_validation.runs",
	}
	for _, name := range want {
		if _, ok := metrics[name]; !ok {
			t.Errorf("missing metric %q", name)
		}
	}
	if len(metrics) != len(want) {
		for name := range metrics {
			t.Logf("published: %s", name)
		}
		t.Errorf("published %d metrics, want %d", len(metrics), len(want))
	}
}

// allSeverityNames is every value health.Severity can take; each severity
// gauge reports all of them per subject.
var allSeverityNames = []string{"PASS", "INFO", "WARNING", "CRITICAL", "UNKNOWN"}

// activeSeverity returns the severity at 1, failing unless exactly one is and
// the rest are 0.
func activeSeverity(t *testing.T, points []*metricspb.NumberDataPoint, subject string) string {
	t.Helper()
	var active []string
	seen := map[string]bool{}
	for _, dp := range points {
		name := attrs(dp.GetAttributes())["severity"]
		seen[name] = true
		switch dp.GetAsInt() {
		case 1:
			active = append(active, name)
		case 0:
		default:
			t.Errorf("%s: severity %q = %d, want 0 or 1", subject, name, dp.GetAsInt())
		}
	}
	for _, name := range allSeverityNames {
		if !seen[name] {
			t.Errorf("%s: no point for severity %q; a recovered subject would keep "+
				"reading at its old severity", subject, name)
		}
	}
	if len(active) != 1 {
		t.Fatalf("%s: severities at 1 = %v, want exactly one", subject, active)
	}
	return active[0]
}

// TestPublishRecordsSeverityAsLabel guards the wire contract dashboards read:
// severity is a name on the point, not an ordinal in its value.
func TestPublishRecordsSeverityAsLabel(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	metrics := cap.metricsByName(t)

	node := metrics["health_validation.node.severity"].GetGauge().GetDataPoints()
	if got := activeSeverity(t, node, "node"); got != "CRITICAL" {
		t.Errorf("node severity = %q, want CRITICAL", got)
	}

	byCheck := map[string][]*metricspb.NumberDataPoint{}
	for _, dp := range metrics["health_validation.check.severity"].GetGauge().GetDataPoints() {
		a := attrs(dp.GetAttributes())
		byCheck[a["check_id"]] = append(byCheck[a["check_id"]], dp)
		if a["severity"] == "" {
			t.Errorf("check %q point carries no severity attribute", a["check_id"])
		}
	}
	if len(byCheck) != 2 {
		t.Fatalf("checks reported = %d, want 2", len(byCheck))
	}
	if got := activeSeverity(t, byCheck["gpu_ecc_errors"], "gpu_ecc_errors"); got != "CRITICAL" {
		t.Errorf("gpu_ecc_errors severity = %q, want CRITICAL", got)
	}
	if got := activeSeverity(t, byCheck["gpu_count"], "gpu_count"); got != "PASS" {
		t.Errorf("gpu_count severity = %q, want PASS", got)
	}

	a := attrs(byCheck["gpu_ecc_errors"][0].GetAttributes())
	if a["category"] != health.GPU.String() || a["nvidia_endorsed"] != "true" {
		t.Errorf("gpu_ecc_errors attributes = %v", a)
	}
}

// TestPublishRecordsDomainSeverityAsLabel covers the gauge keyed by category.
func TestPublishRecordsDomainSeverityAsLabel(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	byCategory := map[string][]*metricspb.NumberDataPoint{}
	for _, dp := range cap.metricsByName(t)["health_validation.domain.severity"].GetGauge().GetDataPoints() {
		category := attrs(dp.GetAttributes())["category"]
		byCategory[category] = append(byCategory[category], dp)
	}
	if got := activeSeverity(t, byCategory[health.GPU.String()], "gpu domain"); got != "CRITICAL" {
		t.Errorf("gpu domain severity = %q, want CRITICAL", got)
	}
	if got := activeSeverity(t, byCategory[health.Node.String()], "node domain"); got != "PASS" {
		t.Errorf("node domain severity = %q, want PASS", got)
	}
}

// TestPublishReportsSkipsAndEntities covers the per-check instruments:
// skipped checks are reported separately from executed ones, and
// affected_entities reports a count for every check that ran — including the
// zero that contradicts a previous run's non-zero count.
func TestPublishReportsSkipsAndEntities(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	metrics := cap.metricsByName(t)

	skipped := metrics["health_validation.check.skipped"].GetGauge().GetDataPoints()
	if len(skipped) != 1 {
		t.Fatalf("skipped points = %d, want 1", len(skipped))
	}
	a := attrs(skipped[0].GetAttributes())
	if a["check_id"] != "nvlink_active" || a["skip_reason"] != health.SensorMissing.String() {
		t.Errorf("skip attributes = %v", a)
	}
	if got := skipped[0].GetAsInt(); got != 1 {
		t.Errorf("skipped value = %d, want 1", got)
	}

	// One point per check that ran: 2 for the failing check, and an explicit
	// 0 for the passing one. The skipped check is absent — it named nothing
	// because it never ran, which is what check.skipped reports.
	entities := byCheckID(t, metrics["health_validation.check.affected_entities"].GetGauge().GetDataPoints())
	if len(entities) != 2 {
		t.Fatalf("affected_entities points = %d, want 2 (one per executed check)", len(entities))
	}
	if got := entities["gpu_ecc_errors"]; got != 2 {
		t.Errorf("gpu_ecc_errors affected_entities = %d, want 2", got)
	}
	if got, ok := entities["gpu_count"]; !ok || got != 0 {
		t.Errorf("gpu_count affected_entities = %d (present=%v), want an explicit 0", got, ok)
	}
}

// byCheckID indexes gauge points by their check_id attribute.
func byCheckID(t *testing.T, points []*metricspb.NumberDataPoint) map[string]int64 {
	t.Helper()
	out := make(map[string]int64, len(points))
	for _, pt := range points {
		out[attrs(pt.GetAttributes())["check_id"]] = pt.GetAsInt()
	}
	return out
}

// TestRunsCounterUsesDeltaTemporality guards the one-shot contract: a fresh
// process must not report a cumulative total that looks like a reset.
func TestRunsCounterUsesDeltaTemporality(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	sum := cap.metricsByName(t)["health_validation.runs"].GetSum()
	if sum.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
		t.Errorf("temporality = %v, want DELTA", sum.GetAggregationTemporality())
	}
	points := sum.GetDataPoints()
	if len(points) != 1 || points[0].GetAsInt() != 1 {
		t.Fatalf("runs points = %v, want a single 1", points)
	}
	if a := attrs(points[0].GetAttributes()); a["severity"] != health.Critical.String() {
		t.Errorf("runs attributes = %v, want severity=CRITICAL", a)
	}
}

// TestResourceCarriesIdentity keeps node and version identity on the resource
// rather than on every data point, and proves an operator cannot overwrite the
// attribute a collector routes on.
func TestResourceCarriesIdentity(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	err := publish(t, srv.URL, otelmetrics.Options{
		NodeID:         "gpu-node-1",
		ServiceVersion: "v1.2.3",
		CatalogVersion: "v0.2.0",
		ResourceAttributes: map[string]string{
			"cluster": "dsx-east",
			// An operator must not be able to masquerade as another service.
			"service.name": "not-health-validation",
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := cap.resourceAttrs(t)
	for key, want := range map[string]string{
		"service.name":                      otelmetrics.ServiceName,
		"service.version":                   "v1.2.3",
		"service.instance.id":               "gpu-node-1",
		"health_validation.catalog_version": "v0.2.0",
		"health_validation.schema_version":  health.NodeVerdictSchemaVersion,
		"cluster":                           "dsx-east",
	} {
		if got[key] != want {
			t.Errorf("resource[%q] = %q, want %q", key, got[key], want)
		}
	}
}

// TestPublishSendsHeaders covers authenticated endpoints (a cloud or vendor
// collector), where a missing header is a silent 401 at 3am.
func TestPublishSendsHeaders(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	err := publish(t, srv.URL, otelmetrics.Options{
		Headers: map[string]string{"X-Api-Key": "secret-token"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := cap.headers[0].Get("X-Api-Key"); got != "secret-token" {
		t.Errorf("X-Api-Key = %q, want secret-token", got)
	}
}

// TestPublishFailsOnCollectorError is what the CLI relies on to log a warning:
// a rejecting collector must surface as an error, not a silent success.
func TestPublishFailsOnCollectorError(t *testing.T) {
	srv, _ := newCollector(t, http.StatusInternalServerError)

	err := publish(t, srv.URL, otelmetrics.Options{})
	if err == nil {
		t.Fatal("Publish: got nil error, want an export failure")
	}
	if !strings.Contains(err.Error(), "export metrics") {
		t.Errorf("error = %v, want it to name the export failure", err)
	}
}

// TestPublishDoesNotRetry guards the timeout budget: the stock exporter retries
// with backoff for up to a minute, which would hold a health run open long past
// its configured timeout.
func TestPublishDoesNotRetry(t *testing.T) {
	srv, cap := newCollector(t, http.StatusServiceUnavailable)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err == nil {
		t.Fatal("Publish: got nil error, want an export failure")
	}
	if len(cap.requests) != 1 {
		t.Errorf("collector saw %d attempts, want exactly 1 (retries disabled)", len(cap.requests))
	}
}

// TestNewRejectsBadEndpoint keeps the "host:port" misread — the form the
// collector's own receiver config uses — from becoming a runtime surprise.
func TestNewRejectsBadEndpoint(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:44318", "grpc://host:4317", "", "http://"} {
		if _, err := otelmetrics.New(context.Background(), otelmetrics.Options{Endpoint: endpoint}); err == nil {
			t.Errorf("New(%q): got nil error, want a rejection", endpoint)
		}
	}
}

// TestPublishTrimsEndpoint covers the quoted-YAML case: url.Parse rejects
// surrounding whitespace, so an endpoint that passed config validation would
// otherwise fail at publish time.
func TestPublishTrimsEndpoint(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, "  "+srv.URL+"  ", otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(cap.paths) != 1 || cap.paths[0] != "/v1/metrics" {
		t.Errorf("paths = %v, want one /v1/metrics", cap.paths)
	}
}

// TestEnvHeadersDoNotLeak is the regression guard for a credential leak: the
// exporter applies OTEL_EXPORTER_OTLP_* before the options it is given, so an
// unset option inherits the node's environment. Both variables are asserted
// because the signal-specific one is read independently of the generic one.
func TestEnvHeadersDoNotLeak(t *testing.T) {
	for _, envVar := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS"} {
		t.Run(envVar, func(t *testing.T) {
			t.Setenv(envVar, "x-leaked-token=super-secret")
			srv, cap := newCollector(t, http.StatusOK)

			// No Headers in Options: where an unset option would inherit the variable.
			if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if len(cap.headers) != 1 {
				t.Fatalf("got %d exports, want 1", len(cap.headers))
			}
			if got := cap.headers[0].Get("X-Leaked-Token"); got != "" {
				t.Errorf("%s leaked into the request as %q; configured headers must "+
					"replace the environment, not fall back to it", envVar, got)
			}
		})
	}
}

// TestEnvEndpointDoesNotOverride pins the other half: a stray variable cannot
// redirect a node's telemetry elsewhere.
func TestEnvEndpointDoesNotOverride(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://192.0.2.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://192.0.2.1:4318")
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(cap.requests) != 1 {
		t.Errorf("configured collector saw %d exports, want 1", len(cap.requests))
	}
}

// perGPUVerdict is one check fanned out across four GPUs, the shape
// evaluateAllRaw produces for a per-entity check: same check_id on every
// verdict, severity varying by entity, and the failing GPU not last.
func perGPUVerdict() health.NodeVerdict {
	now := time.Now()
	results := []health.CheckVerdict{
		{Timestamp: now, CheckID: "gpu_ecc_errors", Category: health.GPU, Severity: health.Pass},
		{
			Timestamp: now, CheckID: "gpu_ecc_errors", Category: health.GPU,
			Severity: health.Critical, AffectedEntities: []string{"GPU-1"},
		},
		{Timestamp: now, CheckID: "gpu_ecc_errors", Category: health.GPU, Severity: health.Pass},
		{Timestamp: now, CheckID: "gpu_ecc_errors", Category: health.GPU, Severity: health.Pass},
	}
	return health.NodeVerdict{
		SchemaVersion:    health.NodeVerdictSchemaVersion,
		Timestamp:        now,
		NodeID:           "gpu-node-1",
		OverallSeverity:  health.Critical,
		SeverityByDomain: map[health.Category]health.Severity{health.GPU: health.Critical},
		CheckResults:     results,
	}
}

// TestPerEntityResultsDoNotOverwrite is the regression guard for a silently
// lost fault. A per-entity check yields one verdict per GPU, all sharing
// check_id and every other attribute, so recording each one separately makes
// them collide in a single gauge series: the last GPU written wins and one
// critical GPU among healthy ones disappears.
func TestPerEntityResultsDoNotOverwrite(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	ctx := context.Background()
	p, err := otelmetrics.New(ctx, otelmetrics.Options{Endpoint: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	if err := p.Publish(ctx, perGPUVerdict()); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	points := cap.metricsByName(t)["health_validation.check.severity"].GetGauge().GetDataPoints()
	if got := activeSeverity(t, points, "gpu_ecc_errors"); got != "CRITICAL" {
		t.Errorf("gpu_ecc_errors severity = %q, want CRITICAL: the failing GPU must "+
			"survive the three passing ones recorded around it", got)
	}

	// Four per-GPU verdicts fold into one check, so one point — counting the
	// single GPU that failed.
	entities := cap.metricsByName(t)["health_validation.check.affected_entities"].GetGauge().GetDataPoints()
	if len(entities) != 1 || entities[0].GetAsInt() != 1 {
		t.Errorf("affected_entities = %v, want a single point of 1", entities)
	}
}

// TestAffectedEntitiesFromRealEvaluation closes the gap that let this metric
// ship unpublishable: every other test here hand-builds verdicts, so they
// passed while the evaluation engine never populated AffectedEntities and the
// real binary emitted five metrics instead of six. This one drives a catalog
// evaluation end to end.
func TestAffectedEntitiesFromRealEvaluation(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(health.CheckDefinition{
		ID:                "gpu_pstate_probe",
		Category:          health.GPU,
		RequiredSignals:   []string{gpu.SignalGPUID, gpu.SignalGPUPstate},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules: []health.Rule{{
			Expression: "gpu_pstate != 0", Severity: health.Critical, Message: "wrong pstate",
		}},
	})

	nv, err := cat.EvaluateAll(
		gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0},
		gpu.GPUMeasurement{GPUID: "GPU-1", GPUPstate: 8},
	)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}

	srv, cap := newCollector(t, http.StatusOK)
	ctx := context.Background()
	p, err := otelmetrics.New(ctx, otelmetrics.Options{Endpoint: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	if err := p.Publish(ctx, nv); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	m := cap.metricsByName(t)["health_validation.check.affected_entities"]
	if m == nil {
		t.Fatal("affected_entities was not published for a verdict with a failing GPU")
	}
	points := m.GetGauge().GetDataPoints()
	if len(points) != 1 || points[0].GetAsInt() != 1 {
		t.Errorf("affected_entities = %v, want a single point of 1", points)
	}
}
