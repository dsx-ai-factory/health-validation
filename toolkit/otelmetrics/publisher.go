// Package otelmetrics publishes a [health.NodeVerdict] to an OTLP/HTTP
// metrics endpoint.
//
// Built for the one-shot `health-validation run`: record one verdict, export,
// exit. It does not register a global MeterProvider, so an SDK consumer
// embedding the CLI keeps its own telemetry wiring. Export failures are the
// caller's to swallow — a health verdict must not change because a collector
// was unreachable.
package otelmetrics

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// ServiceName is fixed: downstream collectors route on it.
const ServiceName = "health-validation"

const scopeName = "github.com/dsx-ai-factory/health-validation/toolkit/otelmetrics"

// defaultTimeout is the whole export budget, not one attempt's: retries are
// disabled in [New].
const defaultTimeout = 10 * time.Second

// severities is reported in full on every severity gauge — 1 for the severity
// in effect, 0 for the rest. Emitting only the active one would leave the
// previous run's point sitting at 1 with nothing to contradict it, so a
// recovered node would still read as CRITICAL.
var severities = []health.Severity{
	health.Pass, health.Info, health.Warning, health.Critical, health.Unknown,
}

// Spelled out rather than imported from otel/semconv: the keys are stable, and
// the versioned package would pin the Bazel label to semconv/v1.37.0, a moving
// target on every OTel bump.
const (
	schemaURL         = "https://opentelemetry.io/schemas/1.37.0"
	attrServiceName   = "service.name"
	attrServiceVer    = "service.version"
	attrServiceInstID = "service.instance.id"
)

// Options configures a [Publisher]. Only Endpoint is required. It is a plain
// struct rather than the CLI's config type so this package does not depend on
// the YAML shape.
type Options struct {
	Headers            map[string]string
	ResourceAttributes map[string]string
	Endpoint           string // full OTLP/HTTP URL; /v1/metrics is appended when the path is empty
	NodeID             string // becomes service.instance.id
	ServiceVersion     string
	CatalogVersion     string
	Timeout            time.Duration // whole export budget; see defaultTimeout
}

type Publisher struct {
	provider *sdkmetric.MeterProvider
	reader   *sdkmetric.ManualReader
	exporter sdkmetric.Exporter

	nodeSeverity     metric.Int64Gauge
	domainSeverity   metric.Int64Gauge
	checkSeverity    metric.Int64Gauge
	checkSkipped     metric.Int64Gauge
	affectedEntities metric.Int64Gauge
	runs             metric.Int64Counter
}

// New builds a Publisher exporting to opts.Endpoint over OTLP/HTTP.
//
// Retries are disabled: the stock exporter backs off for up to a minute, which
// would hold a health run open long past opts.Timeout. Sums are delta — each
// run is a fresh process, so a cumulative counter would restart at 1 and read
// as a reset.
func New(ctx context.Context, opts Options) (*Publisher, error) {
	endpoint, err := normalizeEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// Every option is passed unconditionally, empty values included. The
	// exporter applies the OTEL_EXPORTER_OTLP_* environment variables *before*
	// these options (oconf.NewHTTPConfig), so any option left unset silently
	// inherits whatever the node's environment carries.
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithHeaders(opts.Headers),
		otlpmetrichttp.WithTimeout(timeout),
		otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}),
	)
	if err != nil {
		return nil, fmt.Errorf("build OTLP metrics exporter: %w", err)
	}

	// ManualReader, not PeriodicReader: the latter flushes again on Shutdown,
	// pushing a second copy of every point at process exit.
	reader := sdkmetric.NewManualReader(sdkmetric.WithTemporalitySelector(deltaSums))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(buildResource(opts)),
		sdkmetric.WithReader(reader),
	)

	p := &Publisher{provider: provider, reader: reader, exporter: exporter}
	if err := p.newInstruments(provider.Meter(scopeName)); err != nil {
		_ = provider.Shutdown(ctx)
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	return p, nil
}

func deltaSums(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	if kind == sdkmetric.InstrumentKindCounter || kind == sdkmetric.InstrumentKindUpDownCounter {
		return metricdata.DeltaTemporality
	}
	return sdkmetric.DefaultTemporalitySelector(kind)
}

// normalizeEndpoint validates the URL and appends the OTLP metrics path when
// only a host was given. A bare "host:port" — the form a collector's own
// receiver config uses, so the likeliest mistake — fails url.Parse rather than
// parsing to a bad scheme, so both cases share one message naming the fix.
func normalizeEndpoint(endpoint string) (string, error) {
	// url.Parse rejects surrounding whitespace, so trim here too or an endpoint
	// that passed the config's trimmed validation fails at publish time.
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf(
			"metrics endpoint %q must be a full URL with an http or https scheme (e.g. http://%s)",
			endpoint, endpoint)
	}
	if u.Host == "" {
		return "", fmt.Errorf("metrics endpoint %q has no host", endpoint)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/metrics"
	}
	return u.String(), nil
}

// buildResource applies operator attributes first so the ones set here win
// (NewWithAttributes is last-wins): fleet context is welcome, spoofing the
// identity a collector routes on is not.
func buildResource(opts Options) *resource.Resource {
	attrs := make([]attribute.KeyValue, 0, len(opts.ResourceAttributes)+5)
	for k, v := range opts.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}
	attrs = append(attrs,
		attribute.String(attrServiceName, ServiceName),
		attribute.String("health_validation.schema_version", health.NodeVerdictSchemaVersion),
	)
	if opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String(attrServiceVer, opts.ServiceVersion))
	}
	if opts.NodeID != "" {
		attrs = append(attrs, attribute.String(attrServiceInstID, opts.NodeID))
	}
	if opts.CatalogVersion != "" {
		attrs = append(attrs, attribute.String("health_validation.catalog_version", opts.CatalogVersion))
	}
	return resource.NewWithAttributes(schemaURL, attrs...)
}

// newInstruments creates everything up front so a bad definition fails at
// construction, where the CLI logs it and skips publishing, rather than
// no-op'ing at record time.
func (p *Publisher) newInstruments(m metric.Meter) error {
	var errs []error
	gauge := func(name, description string) metric.Int64Gauge {
		g, err := m.Int64Gauge(name, metric.WithDescription(description), metric.WithUnit("1"))
		errs = append(errs, err)
		return g
	}
	runs, err := m.Int64Counter("health_validation.runs", metric.WithUnit("1"),
		metric.WithDescription("Completed validation runs, labelled with the overall severity they reported."))

	p.nodeSeverity = gauge("health_validation.node.severity",
		"1 for the severity the node reported, 0 for every other severity.")
	p.domainSeverity = gauge("health_validation.domain.severity",
		"1 for the severity a domain reported, 0 for every other severity.")
	p.checkSeverity = gauge("health_validation.check.severity",
		"1 for the severity a check reported, 0 for every other severity.")
	p.checkSkipped = gauge("health_validation.check.skipped",
		"Always 1, emitted once per check that did not execute, labelled with the skip reason.")
	p.affectedEntities = gauge("health_validation.check.affected_entities",
		"Number of entities (GPUs, links, devices) a check named as affected.")
	p.runs = runs
	return errors.Join(append(errs, err)...)
}

// Publish records nv and exports it, blocking until the export completes,
// fails, or opts.Timeout expires.
func (p *Publisher) Publish(ctx context.Context, nv health.NodeVerdict) error {
	p.record(ctx, nv)

	var rm metricdata.ResourceMetrics
	if err := p.reader.Collect(ctx, &rm); err != nil {
		return fmt.Errorf("collect metrics: %w", err)
	}
	if err := p.exporter.Export(ctx, &rm); err != nil {
		return fmt.Errorf("export metrics: %w", err)
	}
	return nil
}

// record writes one verdict into the instruments. trace_id and span_id are
// deliberately omitted: they are unique per run, so recording them would mint a
// new time series every execution. They stay in the JSON verdict.
func (p *Publisher) record(ctx context.Context, nv health.NodeVerdict) {
	recordSeverity(ctx, p.nodeSeverity, nv.OverallSeverity)
	p.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("severity", nv.OverallSeverity.String()),
	))

	for category, severity := range nv.SeverityByDomain {
		recordSeverity(ctx, p.domainSeverity, severity,
			attribute.String("category", category.String()))
	}

	for _, cv := range nv.SkippedChecks {
		p.checkSkipped.Record(ctx, 1, metric.WithAttributes(
			attribute.String("check_id", cv.CheckID),
			attribute.String("category", cv.Category.String()),
			attribute.String("skip_reason", cv.SkipReason.String()),
		))
	}

	for _, r := range aggregateByCheck(nv.CheckResults) {
		id := []attribute.KeyValue{
			attribute.String("check_id", r.verdict.CheckID),
			attribute.String("category", r.verdict.Category.String()),
		}
		recordSeverity(ctx, p.checkSeverity, r.severity, append(id,
			attribute.String("condition_scope", r.verdict.ConditionScope.String()),
			attribute.String("maturity_level", r.verdict.MaturityLevel.String()),
			attribute.Bool("nvidia_endorsed", r.verdict.NVIDIAEndorsed),
		)...)
		if n := len(r.entities); n > 0 {
			p.affectedEntities.Record(ctx, int64(n), metric.WithAttributes(id...))
		}
	}
}

// checkResult is one check's outcome across every entity it ran against.
// entities precedes verdict to satisfy nogo's pointer-scan metric, not
// largest-first ordering.
type checkResult struct {
	entities map[string]struct{}
	verdict  health.CheckVerdict // first seen; supplies the check-level attributes
	severity health.Severity     // worst across entities
}

// aggregateByCheck folds a check's per-entity verdicts into one.
//
// The catalog evaluates a check once per measurement that feeds it, so a
// per-GPU check yields one verdict per GPU — all carrying the same check_id and
// the same check-level attributes. Recorded individually they land in one gauge
// series and the last entity written wins, so a single failing GPU among
// healthy ones would report as PASS. Returned in first-seen order to keep the
// exported points stable between runs.
func aggregateByCheck(verdicts []health.CheckVerdict) []checkResult {
	byID := make(map[string]*checkResult, len(verdicts))
	order := make([]string, 0, len(verdicts))

	for _, cv := range verdicts {
		r, ok := byID[cv.CheckID]
		if !ok {
			r = &checkResult{verdict: cv, entities: map[string]struct{}{}}
			byID[cv.CheckID] = r
			order = append(order, cv.CheckID)
		}
		// Pass is the zero value and the minimum, so it is a safe identity here.
		r.severity = health.Worse(r.severity, cv.Severity)
		for _, e := range cv.AffectedEntities {
			r.entities[e] = struct{}{}
		}
	}

	out := make([]checkResult, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// recordSeverity reports one subject's severity as a name rather than an
// ordinal; see [severities] for why all of them are emitted.
func recordSeverity(ctx context.Context, g metric.Int64Gauge, active health.Severity, extra ...attribute.KeyValue) {
	for _, s := range severities {
		attrs := make([]attribute.KeyValue, 0, len(extra)+1)
		attrs = append(attrs, extra...)
		attrs = append(attrs, attribute.String("severity", s.String()))

		var value int64
		if s == active {
			value = 1
		}
		g.Record(ctx, value, metric.WithAttributes(attrs...))
	}
}

// Shutdown releases the pipeline without exporting: [Publisher.Publish] is the
// only thing that sends, so a failed publish is reported once, there.
func (p *Publisher) Shutdown(ctx context.Context) error {
	return errors.Join(p.provider.Shutdown(ctx), p.exporter.Shutdown(ctx))
}
