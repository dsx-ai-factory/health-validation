// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package app is the importable implementation of the health-validation
// CLI.
//
// The cmd/health-validation binary is a thin shim over [App.Run]; the
// same builder lets a downstream consumer build their own single binary that
// inherits all of go-health's behavior — config loading, the built-in NVIDIA
// check catalog, the host sensors, and the JSON verdict — while contributing
// their own checks and per-GPU probes:
//
//	func main() {
//		a := app.New().WithStderrLogging().WithChecks(mychecks.All()...)
//		// attach domain-scoped probes to the built-in NVML backend, then
//		// install it for the GPU and NVLink sensors:
//		b := a.NVMLBackend().WithGPUProbe(myGPUProbe{}).WithNVLinkProbe(myNVLinkProbe{})
//		a.WithGPUBackend(b).WithNVLinkBackend(b)
//		os.Exit(a.Run(os.Args[1:], os.Stdout, os.Stderr))
//	}
//
// Each of the six sensors (GPU, node, NVLink, CPU, Xid, InfiniBand) draws from
// its own per-domain backend, defaulting to the shared host NVML backend for
// GPU/node/NVLink, sysfs for CPU and InfiniBand, and the journal (through the
// collect sensor) for Xid. A
// With*Backend setter overrides exactly one domain; the
// node domain has no probe seam (gpu_count is a scalar aggregate), so it is a
// full backend swap. Custom checks register alongside the built-ins as a pure
// pass-through: their author-declared classification (Provenance, …) is
// honored verbatim. The only registration constraint is the catalog's
// existing duplicate-ID guard.
package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/internal/version"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"github.com/dsx-ai-factory/health-validation/toolkit/otelmetrics"
)

// Exit codes returned by Run. The split is verdict-vs-no-verdict: 0/1 report a
// verdict the caller can act on, 2 means one was never produced — so 2 is
// deliberately distinct from an UNKNOWN verdict (1). WARNING maps to 0 for now;
// making that configurable is deferred until there is a concrete need.
const (
	exitOK        = 0 // PASS, INFO, WARNING
	exitUnhealthy = 1 // CRITICAL, UNKNOWN
	exitError     = 2 // config/backend/collection/output failure; no verdict
)

// exitCodeForVerdict applies the exit policy to a produced verdict; the
// no-verdict case (exitError) is the caller's to return.
func exitCodeForVerdict(nv health.NodeVerdict) int {
	switch nv.OverallSeverity {
	case health.Pass, health.Info, health.Warning:
		return exitOK
	default:
		// Critical, Unknown, and any severity added later: fail closed.
		return exitUnhealthy
	}
}

// hostBackend is the platform-split source of the built-in NVML data source.
// On linux it is one [nvml.NVMLBackend] exposed under each per-domain
// interface (a single device enumeration feeds the GPU, node, and NVLink
// sensors); off linux it is a stub that errors at Measure, keeping the command
// buildable (and `version` runnable) while surfacing a clear runtime error if
// `run` is invoked there.
type hostBackend interface {
	gpuBackend() toolkit.GPUBackend
	nodeBackend() toolkit.NodeBackend
	nvlinkBackend() toolkit.NVLinkBackend
	architecture() (string, error)
	close() error
}

// App is the health-validation CLI, assembled with the builder methods and
// run with [App.Run]. It wires six sensors (GPU, node, NVLink, CPU, Xid,
// InfiniBand), each from its own per-domain backend — the shared host NVML
// backend, sysfs, or the journal by default, or a caller-supplied override.
type App struct {
	host     hostBackend
	hostname func() (string, error)
	// gpu, node, nvlink, cpu, xid, rdmaLinkFlap, and ib are per-domain backend
	// overrides set by the matching With*Backend setter. A nil override uses the
	// default: the shared host NVML backend for gpu/node/nvlink, the sysfs
	// collect sensor for cpu, the journal collect sensor for xid, and
	// [toolkit.JournalctlRDMALinkFlap] for rdmaLinkFlap. The generic ib backend
	// has no default and is opt-in.
	gpu          toolkit.GPUBackend
	node         toolkit.NodeBackend
	nvlink       toolkit.NVLinkBackend
	cpu          toolkit.CPUBackend
	xid          toolkit.XidBackend
	rdmaLinkFlap toolkit.RDMALinkFlapBackend
	ib           toolkit.IBBackend
	// prog is the program name used in usage and error-message prefixes. It
	// defaults to filepath.Base(os.Args[0]); see WithProgramName.
	prog            string
	nodeID          string
	customChecks    []health.CheckDefinition
	collectSources  []collectSource
	collectAdapters []CollectAdapter
	// stderrLogging, set via WithStderrLogging, asks Run to install a
	// stderr-backed slog default honoring --verbose.
	stderrLogging bool
	nodeIDSet     bool
}

// New returns an App wired to the production host backend (an NVML backend on
// linux). Construction is side-effect free — NVML init is lazy — so building
// an App is safe on a host without a GPU/driver, and `version` runs there.
func New() *App {
	return &App{
		host:     newHostBackend(),
		hostname: os.Hostname,
	}
}

// WithChecks contributes check definitions on top of the built-in NVIDIA
// catalog. They are registered exactly as given — declared classification
// honored, not overridden — and participate in config-driven threshold
// overrides like built-ins. A duplicate ID makes [App.Run] fail.
func (a *App) WithChecks(defs ...health.CheckDefinition) *App {
	a.customChecks = append(a.customChecks, defs...)
	return a
}

// WithGPUBackend overrides the GPU sensor's backend (default: the shared host
// NVML backend). The canonical use attaches a probe to the built-in NVML
// backend before installing it:
//
//	a.WithGPUBackend(a.NVMLBackend().WithGPUProbe(eccProbe{}))
//
// To drive both per-GPU domains from one probed NVML backend, install it under
// both setters (it satisfies [toolkit.GPUBackend] and [toolkit.NVLinkBackend]):
//
//	b := a.NVMLBackend().WithGPUProbe(myGPU{}).WithNVLinkProbe(myNVLink{})
//	a.WithGPUBackend(b).WithNVLinkBackend(b)
//
// The override is the caller's to manage; see [App.Close].
func (a *App) WithGPUBackend(b toolkit.GPUBackend) *App {
	a.gpu = b
	return a
}

// WithNodeBackend overrides the Node sensor's backend (default: the shared
// host NVML backend, which reports gpu_count). The node domain has no probe
// seam — gpu_count is a scalar aggregate — so this is a full backend swap.
func (a *App) WithNodeBackend(b toolkit.NodeBackend) *App {
	a.node = b
	return a
}

// WithNVLinkBackend overrides the NVLink sensor's backend (default: the shared
// host NVML backend). See [App.WithGPUBackend] for attaching a probe.
func (a *App) WithNVLinkBackend(b toolkit.NVLinkBackend) *App {
	a.nvlink = b
	return a
}

// WithCPUBackend replaces the CPU domain's default collect sensor, which
// reports the Linux kernel's possible and online logical CPU counts and its
// online NUMA node count from sysfs, with a legacy toolkit backend. The
// override is caller-owned.
//
// Deprecated: pass a collect sensor and the CPU signal IDs it replaces to
// [App.WithCollect] instead.
func (a *App) WithCPUBackend(b toolkit.CPUBackend) *App {
	a.cpu = b
	return a
}

// WithXidBackend replaces the collect sensor that reads NVRM Xid events from
// the system journal with a legacy toolkit Xid sensor over b.
//
// Deprecated: pass a collect sensor and the Xid signal IDs it replaces to
// [App.WithCollect] instead.
func (a *App) WithXidBackend(b toolkit.XidBackend) *App {
	a.xid = b
	return a
}

// WithRDMALinkFlapBackend overrides the RDMA link-flap sensor's backend
// (default: [toolkit.JournalctlRDMALinkFlap], which reads mlx5 link transitions
// from the system journal). This sensor feeds the built-in rdma_linkflap_fatal
// check and runs by default, like the Xid sensor.
func (a *App) WithRDMALinkFlapBackend(b toolkit.RDMALinkFlapBackend) *App {
	a.rdmaLinkFlap = b
	return a
}

// WithIBBackend sets the generic InfiniBand sensor's backend. It is opt-in: the
// catalog's only built-in InfiniBand check (rdma_linkflap_fatal) is fed by the
// link-flap sensor, not this one. Supply a backend here to contribute
// additional InfiniBand measurements — the backend owns device enumeration, the
// measurement types it emits, and (with the checks a consumer adds via
// WithChecks) those extra checks.
func (a *App) WithIBBackend(b toolkit.IBBackend) *App {
	a.ib = b
	return a
}

// WithStderrLogging installs a text slog handler writing to Run's stderr, at
// debug level under --verbose and info otherwise. It mutates the process-wide
// slog default; it is how the stock binary keeps its stderr logging. Without
// it, Run leaves the caller's logging untouched.
func (a *App) WithStderrLogging() *App {
	a.stderrLogging = true
	return a
}

// WithProgramName overrides the program name used in the usage banner and
// error prefixes. Defaults to filepath.Base(os.Args[0]).
func (a *App) WithProgramName(name string) *App {
	a.prog = name
	return a
}

// WithNodeID sets the identifier used when --node-id is omitted. If neither is
// supplied, run uses the system hostname.
func (a *App) WithNodeID(id string) *App {
	a.nodeID = id
	a.nodeIDSet = true
	return a
}

// Close shuts down the App's host backend — the built-in NVML connection.
// [App.Run] calls it for the caller, so it is needed only if you assemble an
// App without running it.
//
// Backends installed via the With*Backend setters are the caller's to manage
// and are not closed here: a foreign backend is owned by whoever built it, and
// a probe-derived NVML backend (a.NVMLBackend().WithGPUProbe(…)) is a
// lightweight view the one-shot run discards on exit.
func (a *App) Close() error { return a.host.close() }

// Run is the health-validation CLI entrypoint:
//
//	health-validation [--verbose] run [--node-id <id>] [--config <path>]
//	health-validation [--verbose] version [--short] [--output json]
//
// It parses the top-level flags, dispatches the subcommand, writes output to
// stdout and diagnostics to stderr, and returns the process exit code.
func (a *App) Run(args []string, stdout, stderr io.Writer) int {
	if a.prog == "" {
		a.prog = filepath.Base(os.Args[0])
	}

	fs := flag.NewFlagSet(a.prog, flag.ContinueOnError)
	fs.SetOutput(stderr)
	verbose := fs.Bool("verbose", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	a.initLogger(*verbose, stderr)

	return a.dispatch(fs.Args(), stdout, stderr)
}

// initLogger installs a stderr-backed slog default only when the caller opted
// in via WithStderrLogging; otherwise the global default is left untouched.
func (a *App) initLogger(verbose bool, stderr io.Writer) {
	if !a.stderrLogging {
		return
	}
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// dispatch routes the top-level subcommand.
func (a *App) dispatch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage(a.prog))
		return exitError
	}

	switch args[0] {
	case "collect":
		return a.collectCommand(args[1:], stdout, stderr)
	case "run":
		return a.runCommand(args[1:], stdout, stderr)
	case "version":
		return versionCommand(args[1:], stdout, stderr, a.prog)
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n%s\n", a.prog, args[0], usage(a.prog))
		return exitError
	}
}

// runCommand parses `run` flags, builds the catalog (applying any config
// overrides and custom checks), and evaluates it against the six domain
// sensors. Each sensor's backend is the caller's override if set, else the
// default: the shared host NVML backend for gpu/node/nvlink, sysfs for cpu and
// infiniband, and the system journal, through the collect sensor, for xid.
func (a *App) runCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(a.prog+" run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "",
		"path to a YAML config file that parameterizes and disables checks (optional)")
	nodeID := fs.String("node-id", a.nodeID,
		"identifier of the node under test (overrides the configured default or system hostname)")
	cadenceFlag := fs.String("cadence", "",
		"evaluate checks whose scheduling cadence covers this window (CONTINUOUS, PROLOG, MAINTENANCE); omit to run every registered check")
	executionContextFlag := fs.String("execution-context", health.TenantGuest.String(),
		"execution context used to select checks (OPERATOR_INFRA, TENANT_GUEST, OUT_OF_BAND)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", a.prog, fs.Arg(0))
		return exitError
	}

	nodeIDSet := a.nodeIDSet
	cadenceSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "node-id" {
			nodeIDSet = true
		}
		if f.Name == "cadence" {
			cadenceSet = true
		}
	})
	resolvedNodeID, err := a.resolveNodeID(*nodeID, nodeIDSet)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	var cadence *health.SchedulingCadence
	if cadenceSet {
		sc, parseErr := health.SchedulingCadenceFromName(strings.ToUpper(strings.TrimSpace(*cadenceFlag)))
		if parseErr != nil {
			fmt.Fprintf(stderr, "%s: --cadence: %v\n", a.prog, parseErr)
			return exitError
		}
		cadence = &sc
	}
	executionContext, err := health.ExecutionContextFromName(strings.ToUpper(strings.TrimSpace(*executionContextFlag)))
	if err != nil {
		fmt.Fprintf(stderr, "%s: --execution-context: %v\n", a.prog, err)
		return exitError
	}

	// Close only the host backend: per-domain overrides are caller-owned (see
	// [App.Close]). gpu/node/nvlink default to the one shared host backend, so
	// the no-override path enumerates NVML once. Deferred before architecture
	// detection so the NVML handle that detection opens is always released.
	defer a.host.close()

	// Load the config before touching the hardware so a typo is reported as a
	// config error rather than surfacing after a full collection — and so the
	// metrics settings are in hand even on the discovery-failure path below,
	// which is exactly the outcome worth publishing.
	cfg, err := loadOptionalConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}

	// Detect the node architecture so architecture-scoped checks run only
	// where they apply. A discovery failure is a node fault — the GPUs are
	// unenumerable or unreadable — so drain for that explicit reason instead
	// of evaluating with no architecture (Evaluate requires WithArchitecture).
	arch, err := a.host.architecture()
	if err != nil || arch == "" {
		if arch == "" {
			err = fmt.Errorf("architecture is empty")
		}
		slog.Warn("architecture discovery failed", "err", err)
		verdict := discoveryFailureVerdict(err)
		code := writeVerdict(verdict, a.prog, resolvedNodeID, stdout, stderr)
		publishMetrics(cfg.Metrics, verdict, resolvedNodeID)
		return code
	}

	cat, timeout, err := buildCatalog(cfg, a.customChecks, arch, executionContext)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}

	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{Cadence: cadence})
	if err != nil {
		fmt.Fprintf(stderr, "%s: select runnable checks: %v\n", a.prog, err)
		return exitError
	}
	sensors, err := a.sensors(timeout, requiredFor(runnable))
	if err != nil {
		fmt.Fprintf(stderr, "%s: register collect sensors: %v\n", a.prog, err)
		return exitError
	}
	verdict, produced, code := evaluate(cat, toolkit.CollectionOptions{
		Cadence: cadence,
		Timeout: timeout,
	}, a.prog, resolvedNodeID, stdout, stderr, sensors...)
	if produced {
		publishMetrics(cfg.Metrics, verdict, resolvedNodeID)
	}
	return code
}

func (a *App) sensors(timeout time.Duration, required requiredSignals) ([]toolkit.Sensor, error) {
	gpu := a.gpu
	if gpu == nil {
		gpu = a.host.gpuBackend()
	}
	node := a.node
	if node == nil {
		node = a.host.nodeBackend()
	}
	nvlink := a.nvlink
	if nvlink == nil {
		nvlink = a.host.nvlinkBackend()
	}
	rdmaLinkFlap := a.rdmaLinkFlap
	if rdmaLinkFlap == nil {
		rdmaLinkFlap = toolkit.JournalctlRDMALinkFlap()
	}

	sensors := []toolkit.Sensor{
		toolkit.NewGPUSensor(gpu),
		toolkit.NewNodeSensor(node),
		toolkit.NewNVLinkSensor(nvlink),
	}
	// Without an override, the Xid domain comes from builtinCollect.
	if a.xid != nil {
		sensors = append(sensors, toolkit.NewXidSensor(a.xid))
	}
	sensors = append(sensors, toolkit.NewRDMALinkFlapSensor(rdmaLinkFlap))
	// The generic InfiniBand sensor is opt-in: the catalog's built-in
	// InfiniBand check (rdma_linkflap_fatal) is fed by the link-flap sensor
	// above, so this one runs only when a consumer supplies a backend (and
	// its own checks) via WithIBBackend.
	if a.ib != nil {
		sensors = append(sensors, toolkit.NewIBSensor(a.ib))
	}
	// Without an override, the CPU domain comes from builtinCollect.
	if a.cpu != nil {
		sensors = append(sensors, toolkit.NewCPUSensor(a.cpu))
	}
	builtinSources, builtinAdapters := a.builtinCollect()
	collectSensors, err := a.collectSensors(timeout, builtinSources, builtinAdapters, required)
	if err != nil {
		return nil, err
	}
	sensors = append(sensors, collectSensors...)
	return sensors, nil
}

// publishMetrics pushes the verdict to the configured OTLP endpoint. It is a
// no-op when metrics are disabled, and every failure is logged and swallowed:
// a node's health must not read differently because a collector was down. It
// runs after the verdict has already been written to stdout, so a slow
// collector cannot delay the operator's primary output.
func publishMetrics(cfg MetricsConfig, verdict health.NodeVerdict, nodeID string) {
	if !cfg.Enabled {
		return
	}
	timeout := cfg.Timeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	info := version.Get()
	publisher, err := otelmetrics.New(ctx, otelmetrics.Options{
		Headers:            cfg.Headers,
		ResourceAttributes: cfg.ResourceAttributes,
		Endpoint:           cfg.Endpoint,
		NodeID:             nodeID,
		ServiceVersion:     info.Version,
		CatalogVersion:     info.CatalogVersion,
		Timeout:            timeout,
	})
	if err != nil {
		slog.Warn("metrics publishing skipped", "endpoint", redactEndpoint(cfg.Endpoint), "err", err)
		return
	}
	defer func() {
		if err := publisher.Shutdown(ctx); err != nil {
			slog.Debug("metrics publisher shutdown", "err", err)
		}
	}()

	if err := publisher.Publish(ctx, verdict); err != nil {
		slog.Warn("metrics publishing failed", "endpoint", redactEndpoint(cfg.Endpoint), "err", err)
		return
	}
	slog.Debug("metrics published", "endpoint", redactEndpoint(cfg.Endpoint))
}

// redactEndpoint reduces a metrics endpoint to scheme, host, and path for
// logging, which operators capture alongside the verdict. Userinfo and the
// whole query string go: providers pass tokens in both.
func redactEndpoint(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" {
		return "[unparseable]"
	}
	redacted := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if u.User != nil {
		redacted.User = url.User("redacted")
	}
	return redacted.String()
}

func (a *App) resolveNodeID(value string, supplied bool) (string, error) {
	nodeID := strings.TrimSpace(value)
	if supplied {
		if nodeID == "" {
			return "", fmt.Errorf("--node-id must not be empty")
		}
		return nodeID, nil
	}

	hostname, err := a.hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w; pass --node-id to override", err)
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", fmt.Errorf("get hostname: empty value; pass --node-id to override")
	}
	return hostname, nil
}

// usage returns the one-line usage banner for the given program name.
func usage(prog string) string {
	return "usage: " + prog + " <collect|run|version> [flags]"
}

var categoryBySensor = map[string]health.Category{
	"cpu":           health.CPU,
	"gpu":           health.GPU,
	"infiniband":    health.InfiniBand,
	"node":          health.Node,
	"nvlink":        health.NVLink,
	"rdma_linkflap": health.InfiniBand,
	"xid":           health.Xid,
}

// collectCommand runs only the sensors selected by checks listed in config.
func (a *App) collectCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(a.prog+" collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to a YAML config listing checks to collect")
	nodeID := fs.String("node-id", a.nodeID,
		"identifier of the node under test, the target of host-wide observations (overrides the configured default or system hostname)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", a.prog, fs.Arg(0))
		return exitError
	}
	nodeIDSet := a.nodeIDSet
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "node-id" {
			nodeIDSet = true
		}
	})
	resolvedNodeID, err := a.resolveNodeID(*nodeID, nodeIDSet)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	cfg, err := loadOptionalConfig(strings.TrimSpace(*configPath))
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	cat, timeout, err := buildCatalog(cfg, a.customChecks, "", health.TenantGuest)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	categories, err := collectionCategories(cat, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	defer a.host.close()
	// Whole categories, not runnable signals: a batch may be evaluated later
	// under a different cadence or config.
	required := make(requiredSignals, len(categories))
	for category := range categories {
		required[category] = nil
	}
	allSensors, err := a.sensors(timeout, required)
	if err != nil {
		fmt.Fprintf(stderr, "%s: register collect sensors: %v\n", a.prog, err)
		return exitError
	}
	sensors, err := collectionSensors(allSensors, categories)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	batch, err := collectionOutput(uuid.NewString(), resolvedNodeID, toolkit.CollectMeasurementsWithTimeout(timeout, sensors...))
	if err != nil {
		fmt.Fprintf(stderr, "%s: prepare observation batch: %v\n", a.prog, err)
		return exitError
	}
	encoded, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(batch)
	if err != nil {
		fmt.Fprintf(stderr, "%s: encode observation batch: %v\n", a.prog, err)
		return exitError
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintf(stderr, "%s: write observation batch: %v\n", a.prog, err)
		return exitError
	}
	return exitOK
}

func collectionCategories(cat *catalog.Catalog, cfg *Config) (map[health.Category]bool, error) {
	disabled := make(map[string]bool, len(cfg.DisabledChecks))
	for _, id := range cfg.DisabledChecks {
		disabled[strings.TrimSpace(id)] = true
	}
	wanted := make(map[health.Category]bool)
	if len(cfg.Checks) == 0 {
		for _, check := range cat.List(catalog.Filter{}) {
			if !disabled[check.ID] {
				wanted[check.Category] = true
			}
		}
	} else {
		for _, checkID := range sortedKeys(cfg.Checks) {
			if disabled[checkID] {
				continue
			}
			check, err := cat.Get(checkID)
			if err != nil {
				return nil, err
			}
			wanted[check.Category] = true
		}
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("config selects no checks to collect")
	}
	return wanted, nil
}

func collectionSensors(sensors []toolkit.Sensor, categories map[health.Category]bool) ([]toolkit.Sensor, error) {
	selected := make([]toolkit.Sensor, 0, len(categories))
	covered := make(map[health.Category]bool)
	for _, sensor := range sensors {
		category, ok := categoryBySensor[sensor.Name()]
		// Collect adapters declare their category; their names are arbitrary.
		if bridged, isBridged := sensor.(*collectbridge.Sensor); isBridged {
			category, ok = bridged.Category(), true
		}
		if ok && categories[category] {
			selected = append(selected, sensor)
			covered[category] = true
		}
	}
	for category := range categories {
		if !covered[category] {
			return nil, fmt.Errorf("no sensor available for %s checks", category)
		}
	}
	return selected, nil
}

// collectionOutput builds the ObservationBatch the collect command emits,
// attributing each sensor's observations to "toolkit.<sensor>", or to the
// source of a provisional or source-level entity key. Host-wide measurements
// target nodeID.
func collectionOutput(collectionID, nodeID string, groups []toolkit.SensorMeasurements) (*observation.ObservationBatch, error) {
	batch := &observation.ObservationBatch{CollectionId: collectionID}
	for _, group := range groups {
		exported, err := observations.FromMeasurements(group.Sensor, nodeID, group.CollectedAt, group.Measurements...)
		if err != nil {
			return nil, fmt.Errorf("%s sensor: %w", group.Sensor, err)
		}
		batch.Observations = append(batch.Observations, exported...)
	}
	return batch, observation.ValidateBatch(batch)
}

// loadOptionalConfig loads the config at path, or returns the zero config when
// path is empty. It never returns nil, so callers can read cfg.Metrics without
// a nil check on the no-config-file path.
func loadOptionalConfig(path string) (*Config, error) {
	if path == "" {
		return &Config{}, nil
	}
	return LoadConfig(path)
}

// buildCatalog returns a catalog pre-loaded with all built-in checks plus
// any custom checks, and the per-sensor collection timeout to apply. cfg's
// check parameters and disabled_checks are validated against the full check
// set (built-ins + custom) and applied as threshold overrides and disables —
// so a config may target a custom check too — and its
// collection_timeout_seconds (if set) is the timeout; otherwise the toolkit
// default is used. A nil cfg means no config file: built-ins only, default
// timeout.
func buildCatalog(cfg *Config, customChecks []health.CheckDefinition, arch string, executionContext health.ExecutionContext) (*catalog.Catalog, time.Duration, error) {
	// register adds the caller-supplied checks to a freshly built catalog.
	// Classification is passed through untouched; the only failure is the
	// catalog's duplicate-ID guard.
	register := func(c *catalog.Catalog) error {
		for _, cd := range customChecks {
			if err := c.Register(cd); err != nil {
				return fmt.Errorf("registering custom check: %w", err)
			}
		}
		return nil
	}

	// An empty architecture is reserved for backends that cannot detect one.
	// NVML reports "unknown" for unrecognized devices so scoped checks still skip.
	// Evaluate requires a non-empty architecture; the CLI fails discovery before
	// it would construct a catalog with none.
	opts := []catalog.Option{catalog.WithExecutionContext(executionContext)}
	if arch != "" {
		opts = append(opts, catalog.WithArchitecture(arch))
	}

	if cfg == nil {
		c := catalog.New(opts...)
		if err := register(c); err != nil {
			return nil, 0, err
		}
		return c, toolkit.DefaultSensorTimeout, nil
	}

	// Validate the config against a catalog that already includes the custom
	// checks, so an override naming a custom check resolves. Then build the
	// real catalog with the resolved overrides and re-register.
	validation := catalog.New(opts...)
	if err := register(validation); err != nil {
		return nil, 0, err
	}
	overrides, err := cfg.Overrides(validation)
	if err != nil {
		return nil, 0, err
	}

	// Validate that the checks ids we are disabling actually exist. Then
	// add an option to the final build to remove them
	disabledCheckIDs, err := cfg.DisabledCheckIDs(validation)
	if err != nil {
		return nil, 0, err
	}
	opts = append(opts, catalog.WithDisabledChecks(disabledCheckIDs...))

	final := catalog.New(append([]catalog.Option{catalog.WithOverrides(overrides)}, opts...)...)
	if err = register(final); err != nil {
		return nil, 0, err
	}
	return final, cfg.CollectionTimeout(), nil
}

// evaluate runs the sensors through the catalog, prints the node verdict as
// indented JSON, and returns the exit code alongside the verdict. It takes the
// collection options and sensors as arguments so tests can inject per-run
// configuration and mock sensors without hardware. produced is false when
// collection failed before any verdict existed, so the caller knows there is
// nothing worth publishing.
func evaluate(cat *catalog.Catalog, opts toolkit.CollectionOptions, prog, nodeID string, stdout, stderr io.Writer, sensors ...toolkit.Sensor) (verdict health.NodeVerdict, produced bool, code int) {
	verdict, err := toolkit.CollectWithOptions(cat, opts, sensors...)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return health.NodeVerdict{}, false, exitError
	}
	return verdict, true, writeVerdict(verdict, prog, nodeID, stdout, stderr)
}

// writeVerdict prints a node verdict as indented JSON and returns the process
// exit code derived from the verdict severity via [exitCodeForVerdict]; a
// missing node ID, marshal failure, or write failure returns [exitError]
// instead. The precise severity lives in the JSON — the exit code is the
// coarse signal.
func writeVerdict(verdict health.NodeVerdict, prog, nodeID string, stdout, stderr io.Writer) int {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		fmt.Fprintf(stderr, "%s: node ID is required\n", prog)
		return exitError
	}
	verdict.NodeID = nodeID

	b, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "%s: marshal verdict: %v\n", prog, err)
		return exitError
	}
	b = append(b, '\n')
	if _, err := stdout.Write(b); err != nil {
		fmt.Fprintf(stderr, "%s: write verdict: %v\n", prog, err)
		return exitError
	}

	// The precise severity lives in the JSON; the exit code is the coarse signal.
	return exitCodeForVerdict(verdict)
}

// discoveryFailureVerdict builds a Critical node verdict for a run that could
// not determine the GPU architecture — the node's GPUs are unenumerable or
// unreadable. Draining for this explicit reason is safer than the alternatives
// the CLI would otherwise fall into: continuing with an empty architecture
// disables filtering and evaluates every check against the wrong thresholds,
// and letting architecture-scoped checks skip would aggregate an all-GPUs-lost
// node to PASS.
func discoveryFailureVerdict(cause error) health.NodeVerdict {
	cv := health.CheckVerdict{
		Timestamp:         time.Now(),
		CheckID:           "architecture_discovery",
		Category:          health.Node,
		ConditionScope:    health.NodeScope,
		Severity:          health.Critical,
		Messages:          []string{fmt.Sprintf("could not determine GPU architecture: %v", cause)},
		RecommendedAction: health.GenericDrain(),
		MaturityLevel:     health.Stable,
		NVIDIAEndorsed:    true,
	}
	return health.AggregateNodeVerdict([]health.CheckVerdict{cv})
}
