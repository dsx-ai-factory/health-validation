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

// TestPublishRecordsSeverityOrdinals guards the wire contract downstream
// dashboards read: the gauge value is the severity ordinal.
func TestPublishRecordsSeverityOrdinals(t *testing.T) {
	srv, cap := newCollector(t, http.StatusOK)

	if err := publish(t, srv.URL, otelmetrics.Options{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	metrics := cap.metricsByName(t)

	node := metrics["health_validation.node.severity"].GetGauge().GetDataPoints()
	if len(node) != 1 {
		t.Fatalf("node severity points = %d, want 1", len(node))
	}
	if got := node[0].GetAsInt(); got != int64(health.Critical) {
		t.Errorf("node severity = %d, want %d (CRITICAL)", got, int64(health.Critical))
	}

	for _, dp := range metrics["health_validation.check.severity"].GetGauge().GetDataPoints() {
		a := attrs(dp.GetAttributes())
		switch a["check_id"] {
		case "gpu_ecc_errors":
			if got := dp.GetAsInt(); got != int64(health.Critical) {
				t.Errorf("gpu_ecc_errors severity = %d, want CRITICAL", got)
			}
			if a["category"] != health.GPU.String() || a["nvidia_endorsed"] != "true" {
				t.Errorf("gpu_ecc_errors attributes = %v", a)
			}
		case "gpu_count":
			if got := dp.GetAsInt(); got != int64(health.Pass) {
				t.Errorf("gpu_count severity = %d, want PASS", got)
			}
		default:
			t.Errorf("unexpected check_id %q", a["check_id"])
		}
	}
}

// TestPublishReportsSkipsAndEntities covers the two instruments that are
// conditional: skipped checks are reported separately from executed ones, and
// affected_entities is emitted only for a check that named entities.
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

	entities := metrics["health_validation.check.affected_entities"].GetGauge().GetDataPoints()
	if len(entities) != 1 {
		t.Fatalf("affected_entities points = %d, want 1 (only the failing check)", len(entities))
	}
	if got := entities[0].GetAsInt(); got != 2 {
		t.Errorf("affected_entities = %d, want 2", got)
	}
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
