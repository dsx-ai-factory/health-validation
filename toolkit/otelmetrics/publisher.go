// Package otelmetrics publishes a [health.NodeVerdict] to an OTLP/HTTP
// metrics endpoint.
//
// It is built for the one-shot `health-validation run`: a Publisher records
// every instrument from a single verdict and flushes synchronously, so the
// process can exit as soon as Publish returns. Nothing here is a background
// pipeline — there is no periodic collection and no global MeterProvider
// registration, so an SDK consumer embedding the CLI keeps its own telemetry
// wiring untouched.
//
// Export failures are the caller's to swallow: a health verdict must not
// change because a collector was unreachable.
package otelmetrics

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// ServiceName is the service.name resource attribute on every published
// metric. It is fixed: a downstream collector routes on it.
const ServiceName = "health-validation"

// scopeName is the instrumentation scope reported with every instrument.
const scopeName = "github.com/dsx-ai-factory/health-validation/toolkit/otelmetrics"

// Options configures a [Publisher]. Endpoint is required; everything else is
// optional. It is a plain struct rather than the config type from toolkit/app
// so this package stays independent of the CLI's YAML shape.
type Options struct {
	// Headers are sent on the OTLP request, for endpoints that authenticate
	// (a vendor or cloud collector); empty for a local collector.
	Headers map[string]string
	// ResourceAttributes are operator-supplied resource attributes, merged
	// under the attributes this package sets — see [New].
	ResourceAttributes map[string]string
	// Endpoint is the full OTLP/HTTP URL, e.g. http://127.0.0.1:44318. A URL
	// with no path gets the conventional /v1/metrics appended; an explicit
	// path is used verbatim. An https scheme enables TLS.
	Endpoint string
	// NodeID identifies the node under test; it becomes service.instance.id.
	NodeID string
	// ServiceVersion is the toolkit version, reported as service.version.
	ServiceVersion string
	// CatalogVersion is the check-catalog version compiled into the producer.
	CatalogVersion string
	// Timeout bounds the export. Retries are disabled, so this is the true
	// ceiling on how long Publish can block.
	Timeout time.Duration
}

// Publisher holds the metric pipeline for one process. Build it with [New] and
// release it with [Publisher.Shutdown].
type Publisher struct {
	provider *sdkmetric.MeterProvider
	reader   *sdkmetric.ManualReader
	exporter sdkmetric.Exporter
	inst     instruments
}

// instruments is the full set of metrics this package reports. Every
// instrument is created up front so a recording failure cannot be mistaken for
// an absent metric.
type instruments struct {
	nodeSeverity     metric.Int64Gauge
	domainSeverity   metric.Int64Gauge
	checkSeverity    metric.Int64Gauge
	checkSkipped     metric.Int64Gauge
	affectedEntities metric.Int64Gauge
	runs             metric.Int64Counter
}

// New builds a Publisher exporting to opts.Endpoint over OTLP/HTTP.
//
// Resource attributes are assembled operator-first: opts.ResourceAttributes is
// applied first and the attributes this package owns (service.name,
// service.version, service.instance.id, and the catalog/schema versions) are
// merged over it, so an operator can add fleet context like a cluster or region
// but cannot spoof the identity a collector routes on.
//
// Deltas, not cumulative sums: each run is a fresh process, so a cumulative
// counter would restart at 1 every time and read as a reset to the backend.
//
// Retries are disabled. The stock exporter retries with backoff for up to a
// minute, which would let an unreachable collector hold a health check open far
// past its configured timeout.
func New(ctx context.Context, opts Options) (*Publisher, error) {
	endpoint, err := normalizeEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}

	exporterOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}),
		otlpmetrichttp.WithTemporalitySelector(deltaTemporality),
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
	// Shutdown, which would push a second copy of every gauge at process exit
	// and double-count the runs counter.
	reader := sdkmetric.NewManualReader(sdkmetric.WithTemporalitySelector(deltaTemporality))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(buildResource(opts)),
		sdkmetric.WithReader(reader),
	)

	inst, err := newInstruments(provider.Meter(scopeName))
	if err != nil {
		// Drop the pipeline rather than leak the exporter's connection because
		// instrument creation failed.
		_ = provider.Shutdown(ctx)
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	return &Publisher{provider: provider, reader: reader, exporter: exporter, inst: inst}, nil
}

// deltaTemporality reports sums as deltas and everything else with the SDK
// default; see [New].
func deltaTemporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	if kind == sdkmetric.InstrumentKindCounter || kind == sdkmetric.InstrumentKindUpDownCounter {
		return metricdata.DeltaTemporality
	}
	return sdkmetric.DefaultTemporalitySelector(kind)
}

// normalizeEndpoint validates the endpoint URL and appends the conventional
// OTLP metrics path when the operator gave only a host. A bare "host:port"
// (the form the collector's own config uses) parses as a URL with the host as
// its scheme, so it is rejected with the fix spelled out.
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
func buildResource(opts Options) *resource.Resource {
	attrs := make([]attribute.KeyValue, 0, len(opts.ResourceAttributes)+5)
	for k, v := range opts.ResourceAttributes {
		attrs = append(attrs, attribute.String(k, v))
	}
	attrs = append(attrs,
		semconv.ServiceName(ServiceName),
		attribute.String("health_validation.schema_version", health.NodeVerdictSchemaVersion),
	)
	if opts.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(opts.ServiceVersion))
	}
	if opts.NodeID != "" {
		attrs = append(attrs, semconv.ServiceInstanceID(opts.NodeID))
	}
	if opts.CatalogVersion != "" {
		attrs = append(attrs, attribute.String("health_validation.catalog_version", opts.CatalogVersion))
	}
	// NewWithAttributes de-duplicates last-wins, which is what puts the
	// attributes above on top of the operator's.
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...)
}

func newInstruments(m metric.Meter) (instruments, error) {
	var (
		inst instruments
		err  error
	)
	if inst.nodeSeverity, err = m.Int64Gauge("health_validation.node.severity",
		metric.WithDescription("Overall node severity as an ordinal: 0 PASS, 1 INFO, 2 WARNING, 3 CRITICAL, 4 UNKNOWN."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create node severity gauge: %w", err)
	}
	if inst.domainSeverity, err = m.Int64Gauge("health_validation.domain.severity",
		metric.WithDescription("Per-domain severity ordinal, one series per check category."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create domain severity gauge: %w", err)
	}
	if inst.checkSeverity, err = m.Int64Gauge("health_validation.check.severity",
		metric.WithDescription("Per-check severity ordinal for the checks that executed."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create check severity gauge: %w", err)
	}
	if inst.checkSkipped, err = m.Int64Gauge("health_validation.check.skipped",
		metric.WithDescription("Always 1, emitted once per check that did not execute, labelled with the skip reason."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create check skipped gauge: %w", err)
	}
	if inst.affectedEntities, err = m.Int64Gauge("health_validation.check.affected_entities",
		metric.WithDescription("Number of entities (GPUs, links, devices) a non-passing check named as affected."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create affected entities gauge: %w", err)
	}
	if inst.runs, err = m.Int64Counter("health_validation.runs",
		metric.WithDescription("Completed validation runs, labelled with the overall severity they reported."),
		metric.WithUnit("1"),
	); err != nil {
		return inst, fmt.Errorf("create runs counter: %w", err)
	}
	return inst, nil
}

// Publish records every instrument from nv and flushes them to the endpoint.
// It blocks until the export completes, fails, or the configured timeout
// expires.
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
// deliberately narrow: node_id and the versions live on the resource, and
// per-run identifiers (trace_id, span_id) are left out entirely — they are
// unique every run, so recording them would mint a new time series per
// execution. They remain available in the JSON verdict.
func (p *Publisher) record(ctx context.Context, nv health.NodeVerdict) {
	p.inst.nodeSeverity.Record(ctx, int64(nv.OverallSeverity))
	p.inst.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("severity", nv.OverallSeverity.String()),
	))

	for category, severity := range nv.SeverityByDomain {
		p.inst.domainSeverity.Record(ctx, int64(severity), metric.WithAttributes(
			attribute.String("category", category.String()),
		))
	}

	for _, cv := range nv.CheckResults {
		p.inst.checkSeverity.Record(ctx, int64(cv.Severity), metric.WithAttributes(
			attribute.String("check_id", cv.CheckID),
			attribute.String("category", cv.Category.String()),
			attribute.String("condition_scope", cv.ConditionScope.String()),
			attribute.String("maturity_level", cv.MaturityLevel.String()),
			attribute.Bool("nvidia_endorsed", cv.NVIDIAEndorsed),
		))
		if len(cv.AffectedEntities) > 0 {
			p.inst.affectedEntities.Record(ctx, int64(len(cv.AffectedEntities)), metric.WithAttributes(
				attribute.String("check_id", cv.CheckID),
				attribute.String("category", cv.Category.String()),
			))
		}
	}

	for _, cv := range nv.SkippedChecks {
		p.inst.checkSkipped.Record(ctx, 1, metric.WithAttributes(
			attribute.String("check_id", cv.CheckID),
			attribute.String("category", cv.Category.String()),
			attribute.String("skip_reason", cv.SkipReason.String()),
		))
	}
}

// Shutdown releases the pipeline. It exports nothing: [Publisher.Publish] is
// the only thing that sends, so a failed publish is reported once, there and
// not again here.
func (p *Publisher) Shutdown(ctx context.Context) error {
	if err := p.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down metrics provider: %w", err)
	}
	if err := p.exporter.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down metrics exporter: %w", err)
	}
	return nil
}
