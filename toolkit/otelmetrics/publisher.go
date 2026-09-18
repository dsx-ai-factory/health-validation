// Package otelmetrics publishes a [health.NodeVerdict] to an OTLP/HTTP
// metrics endpoint.
//
// It is built for the one-shot `health-validation run`: record one verdict,
// export, exit. There is no background pipeline and no global MeterProvider
// registration, so an SDK consumer embedding the CLI keeps its own telemetry
// wiring untouched. Export failures are the caller's to swallow — a health
// verdict must not change because a collector was unreachable.
package otelmetrics

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// ServiceName is the service.name resource attribute on every published
// metric. It is fixed: a downstream collector routes on it.
const ServiceName = "health-validation"

const scopeName = "github.com/dsx-ai-factory/health-validation/toolkit/otelmetrics"

// The three semantic-convention resource attributes this package sets, spelled
// out rather than taken from otel/semconv. The keys are stable across
// convention releases, and importing the versioned package would pin the whole
// build to a directory named after a version (semconv/v1.37.0) — a moving
// target on every OTel bump, for four string constants.
const (
	schemaURL         = "https://opentelemetry.io/schemas/1.37.0"
	attrServiceName   = "service.name"
	attrServiceVer    = "service.version"
	attrServiceInstID = "service.instance.id"
)

// Options configures a [Publisher]. Only Endpoint is required. It is a plain
// struct rather than the CLI's config type so this package stays independent
// of the YAML shape.
type Options struct {
	Headers            map[string]string // sent on the OTLP request, for an endpoint that authenticates
	ResourceAttributes map[string]string // operator-supplied resource attributes; see [New]
	Endpoint           string            // full OTLP/HTTP URL; /v1/metrics is appended when the path is empty
	NodeID             string            // node under test; becomes service.instance.id
	ServiceVersion     string
	CatalogVersion     string
	Timeout            time.Duration // whole export budget: retries are disabled
}

// Publisher holds the metric pipeline for one process. Build it with [New] and
// release it with [Publisher.Shutdown].
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
// run is a fresh process, so a cumulative counter would restart at 1 every time
// and read as a reset.
func New(ctx context.Context, opts Options) (*Publisher, error) {
	endpoint, err := normalizeEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}

	exporterOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}),
	}
	if opts.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithTimeout(opts.Timeout))
	}
	if len(opts.Headers) > 0 {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithHeaders(opts.Headers))
	}
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("build OTLP metrics exporter: %w", err)
	}

	// A ManualReader, not a PeriodicReader: the reader is collected and
	// exported exactly once, by Publish. A PeriodicReader flushes again on
	// Shutdown, which would push a second copy of every gauge at process exit.
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

// normalizeEndpoint validates the URL and appends the conventional OTLP metrics
// path when the operator gave only a host. A bare "host:port" — the form an
// OTLP collector's own receiver config uses, so the likeliest mistake — fails
// url.Parse outright rather than parsing to a bad scheme; both land on the same
// message, which names the fix.
func normalizeEndpoint(endpoint string) (string, error) {
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

// buildResource assembles the resource attached to every published metric.
// Operator attributes are applied first and the ones this package owns are
// merged over them (NewWithAttributes de-duplicates last-wins), so an operator
// can add fleet context but cannot spoof the identity a collector routes on.
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

// newInstruments creates every instrument up front so an invalid definition
// fails at construction — where the CLI logs it and skips publishing — rather
// than silently no-op'ing at record time.
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
		"Overall node severity as an ordinal: 0 PASS, 1 INFO, 2 WARNING, 3 CRITICAL, 4 UNKNOWN.")
	p.domainSeverity = gauge("health_validation.domain.severity",
		"Per-domain severity ordinal, one series per check category.")
	p.checkSeverity = gauge("health_validation.check.severity",
		"Per-check severity ordinal for the checks that executed.")
	p.checkSkipped = gauge("health_validation.check.skipped",
		"Always 1, emitted once per check that did not execute, labelled with the skip reason.")
	p.affectedEntities = gauge("health_validation.check.affected_entities",
		"Number of entities (GPUs, links, devices) a check named as affected.")
	p.runs = runs
	return errors.Join(append(errs, err)...)
}

// Publish records every instrument from nv and exports them. It blocks until
// the export completes, fails, or opts.Timeout expires.
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

// record writes one verdict into the instruments. Attribute sets are kept
// narrow: node ID and versions live on the resource, and the per-run trace_id
// and span_id are left out entirely — they are unique every run, so recording
// them would mint a new time series per execution. They stay in the JSON.
func (p *Publisher) record(ctx context.Context, nv health.NodeVerdict) {
	p.nodeSeverity.Record(ctx, int64(nv.OverallSeverity))
	p.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("severity", nv.OverallSeverity.String()),
	))

	for category, severity := range nv.SeverityByDomain {
		p.domainSeverity.Record(ctx, int64(severity), metric.WithAttributes(
			attribute.String("category", category.String()),
		))
	}

	// Executed and skipped checks are one stream of measurements; which
	// instrument a verdict lands in is the only thing that differs.
	for _, cv := range slices.Concat(nv.CheckResults, nv.SkippedChecks) {
		id := []attribute.KeyValue{
			attribute.String("check_id", cv.CheckID),
			attribute.String("category", cv.Category.String()),
		}
		if cv.Skipped {
			p.checkSkipped.Record(ctx, 1, metric.WithAttributes(
				append(id, attribute.String("skip_reason", cv.SkipReason.String()))...))
			continue
		}
		p.checkSeverity.Record(ctx, int64(cv.Severity), metric.WithAttributes(append(id,
			attribute.String("condition_scope", cv.ConditionScope.String()),
			attribute.String("maturity_level", cv.MaturityLevel.String()),
			attribute.Bool("nvidia_endorsed", cv.NVIDIAEndorsed),
		)...))
		if n := len(cv.AffectedEntities); n > 0 {
			p.affectedEntities.Record(ctx, int64(n), metric.WithAttributes(id...))
		}
	}
}

// Shutdown releases the pipeline. It exports nothing: [Publisher.Publish] is
// the only thing that sends, so a failed publish is reported once, there.
func (p *Publisher) Shutdown(ctx context.Context) error {
	return errors.Join(p.provider.Shutdown(ctx), p.exporter.Shutdown(ctx))
}
