// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	linkflapsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/linkflap"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"github.com/google/uuid"
)

// CollectAdapter converts validated observations into one catalog domain.
// Name, Category, and Signals are captured once per run; adapter names must be
// unique. Signals maps each collect signal ID the adapter reads to the catalog
// signals of Category it fills. A run requests a collect signal only when a
// runnable check of Category requires one of the catalog signals it fills
// (catalog signal names are unique only within a category); an adapter left
// with nothing to request is not built. Build must return only Category measurements;
// any other category fails the adapter's whole output.
type CollectAdapter interface {
	Name() string
	Category() health.Category
	Signals() map[string][]string
	Build([]*observation.Observation) []health.Measurement
	Errored(error) []health.Measurement
}

// WithCollect adds a caller-owned sensor that provides observation facts.
// Adapters registered with [App.WithCollectAdapters] assemble domain
// measurements across all sensors; the two methods may be called in any order.
// Sources and inventory shared by sensors must be constructed by the caller.
// Each sensor must supply at least one adapter-requested signal. A run collects
// only the signals that checks which will run need (see [CollectAdapter]), so
// a disabled check, a cadence outside the window, or an architecture or
// execution-context mismatch also narrows collection; a sensor left with
// nothing to collect is not measured. Registrations are validated against
// every declared signal regardless.
// The sensor supplies exactly signalIDs, which must not be empty, replacing
// the built-in collect source for them; this succeeds the With*Backend
// overrides for collect-backed domains. Claims are explicit so that a later
// release adding built-in sources never changes which sensor supplies a
// signal. Each claimed signal must be declared by the sensor, requested by an
// adapter, and claimed by only one sensor. The Xid signals, and the RDMA
// link-flap signals, are each claimed all together or not at all. A built-in
// source left with no requested signals never runs, and built-in adapters read
// claimed signals from this sensor.
// Each registered collect source has its own timeout budget.
// Timeout blocking is keyed by sensor ID, not the underlying source. Shared
// NVML/DCGM sources must prevent new calls while a timed-out call is running.
// Late results never replace a cached timeout result.
// Each built-in domain is collected by its legacy sensor or by its built-in
// collect sensor, never both. For a domain still on a legacy sensor, override
// its legacy backend with an empty provider when an adapter supplies that
// domain's measurements instead.
func (a *App) WithCollect(sensor collect.Sensor, signalIDs ...string) *App {
	a.collectSources = append(a.collectSources, collectSource{sensor: sensor, claims: slices.Clone(signalIDs)})
	return a
}

// collectSource is a caller sensor and the signals it claims from built-ins.
type collectSource struct {
	sensor collect.Sensor
	claims []string
}

// WithCollectAdapters adds adapters that assemble domain measurements from
// facts across all sensors registered with [App.WithCollect]. Calls append to
// the adapter list; names must be unique across calls. Catalog category names
// (for example "cpu" or "nvlink") are reserved for built-in adapters, whether
// or not that domain's built-in is active yet. A caller adapter may not feed a
// catalog field that an active built-in adapter of its category maps, since
// both would report the same entity; claim the signals with [App.WithCollect]
// instead. A built-in adapter reads its mapped signals from whichever
// registered sensor produces them. Sensor and adapter registration order does not matter; both
// lists are resolved for each run.
// Requested signals without a producer are reported as missing at run time,
// failing only dependent checks rather than causing a registration error.
func (a *App) WithCollectAdapters(adapters ...CollectAdapter) *App {
	a.collectAdapters = append(a.collectAdapters, adapters...)
	return a
}

// GPUAdapter returns a collect adapter that builds one M per GPU for checks
// added with [App.WithChecks]. signals maps each collect signal ID it reads to
// the json-tagged field of M it sets; see [observations.NewGPUConverter] for
// what M needs. Register it with [App.WithCollectAdapters]. A signal the
// built-in NVML sensor reads, such as ECC or row remapping, needs no sensor
// unless [App.WithGPUBackend] replaces the built-in GPU sensors; any other
// needs a sensor registered with [App.WithCollect]. It builds an M only for
// the GPUs its signals report, so a GPU no source reports has no result. A
// signal the built-in GPU adapter also reads, such as the performance state,
// may be read too, but into a field of M's own name: a field the built-in GPU
// measurement reports is rejected at registration.
func GPUAdapter[M health.Measurement](name string, signals map[string]string) (CollectAdapter, error) {
	converter, err := observations.NewGPUConverter[M](signals)
	if err != nil {
		return nil, fmt.Errorf("GPU adapter %q: %w", name, err)
	}
	return gpuAdapter{name: name, converter: converter}, nil
}

type gpuAdapter struct {
	converter *observations.GPUConverter
	name      string
}

func (a gpuAdapter) Name() string                 { return a.name }
func (a gpuAdapter) Category() health.Category    { return a.converter.Category() }
func (a gpuAdapter) Signals() map[string][]string { return a.converter.Feeds() }

// Build fails the adapter's fields when nothing was collected, as the built-in
// adapters do, so a silent source cannot leave its checks skipped and the run
// PASS.
func (a gpuAdapter) Build(obs []*observation.Observation) []health.Measurement {
	if ms := a.converter.ToMeasurements(obs); len(ms) > 0 {
		return ms
	}
	return a.Errored(fmt.Errorf("no %s observations were collected", a.name))
}

func (a gpuAdapter) Errored(err error) []health.Measurement {
	return []health.Measurement{a.converter.Errored("collect."+a.name, err)}
}

// dcgmGPUSignals are the GPU domain's signals the stock CLI reads from DCGM.
// processGPUSignals come from NVML instead: DCGM has no list of the compute
// processes running on a GPU.
var (
	dcgmGPUSignals = collect.Signals{
		gpusignals.PowerDraw, gpusignals.PowerLimitRequested, gpusignals.PowerLimitEnforced,
		gpusignals.PerformanceState, gpusignals.HardwareSlowdownActive,
		gpusignals.HardwareThermalSlowdownActive, gpusignals.HardwarePowerBrakeSlowdownActive,
	}
	processGPUSignals = collect.Signals{gpusignals.ProcessCount, gpusignals.ProcessCmdline}
)

// linkFlapLookback is the window rdma_linkflap_fatal judges, as the legacy
// backend's default was. The CLI sets it rather than inheriting the sensor's
// default, so a change in collect cannot move the check's window.
const linkFlapLookback = time.Hour

// The NVLink domain reads fabric registration from DCGM and everything else
// from NVML. Fabric health stays on NVML: older DCGM releases reject a field
// group holding the health-mask field (174), and the GPU checks share that
// group.
var (
	dcgmNVLinkSignals = collect.Signals{gpusignals.FabricManagerStatus}
	nvmlNVLinkSignals = slices.Concat(collect.Signals{
		gpusignals.NVLinkState, gpusignals.NVLinkLinkCount, gpusignals.NVLinkSpeedCommon,
		gpusignals.NVLinkEndpointSpeed, gpusignals.NVLinkP2PStatus,
		gpusignals.NVLinkFabricBandwidthDegraded, gpusignals.NVLinkFabricRouteRecoveryActive,
		gpusignals.NVLinkFabricRouteUnhealthy, gpusignals.NVLinkFabricAccessTimeoutRecovery,
	}, gpusignals.EndpointCapabilitySignals())
)

// builtinCollect returns the stock CLI's collect-backed domains. A domain
// moves here from the legacy sensor list in runCommand when it migrates; its
// With*Backend override keeps the legacy sensor instead. The node domain and
// the GPU power-draw gate read the host GPU inventory that architecture
// discovery already read in cycle, so they do not enumerate GPUs again.
func (a *App) builtinCollect(cycle *collect.Cycle) ([]collect.Sensor, []CollectAdapter) {
	var sources []collect.Sensor
	var adapters []CollectAdapter
	// The host GPU sensors also feed NVLink; a source left with no requested
	// signals does not run.
	if a.gpu == nil || a.nvlink == nil {
		sources = append(sources, a.host.gpuSensors()...)
	}
	if a.gpu == nil {
		inventory := a.host.gpuInventory()
		adapters = append(adapters, powerDrawGate{
			CollectAdapter: collectadapters.GPU(),
			architecture: func() (string, error) {
				return gpuinventory.Architecture(inventory.Snapshot(context.Background(), cycle))
			},
		})
	}
	if a.cpu == nil {
		sources = append(sources, cpusensor.New(nil))
		adapters = append(adapters, collectadapters.CPU())
	}
	if a.node == nil {
		sources = append(sources, a.host.gpuInventory().Sensor())
		adapters = append(adapters, collectadapters.Node())
	}
	if a.xid == nil {
		sources = append(sources, xidsensor.New(nil))
		// GPUs are named from the inventory discovery or the node domain
		// already read this cycle. The lookup never reads NVML itself, so a
		// wedged driver cannot hold up Xid collection; without a snapshot,
		// each GPU is named by its PCI address.
		inventory := a.host.gpuInventory()
		adapters = append(adapters, collectadapters.XID(func() map[string]string {
			return gpuinventory.UUIDsByPCI(inventory.Cached(cycle))
		}))
	}
	if a.rdmaLinkFlap == nil {
		sources = append(sources, linkflapsensor.New(nil, linkflapsensor.WithLookback(linkFlapLookback)))
		adapters = append(adapters, collectadapters.InfiniBand())
	}
	if a.nvlink == nil {
		adapters = append(adapters, collectadapters.NVLink())
	}
	return sources, adapters
}

// averagedPowerDrawArchitectures are where gpu.power.draw (DCGM board power,
// field 155, and NVML power usage) is the one-second average that
// average_power_draw_w holds. NVML documents it as averaged on Ampere except
// GA100 and newer, and instantaneous on GA100 and older.
var averagedPowerDrawArchitectures = []string{"H100", "H200", "B200", "GB200", "GB300", "VR200"}

// powerDrawGate keeps gpu.power.draw values only where the node architecture
// averages them. Elsewhere a value becomes NOT_SUPPORTED, and with no
// architecture a collection error, so neither is read as an average.
type powerDrawGate struct {
	CollectAdapter
	architecture func() (string, error)
}

func (g powerDrawGate) Build(obs []*observation.Observation) []health.Measurement {
	isPowerDraw := func(o *observation.Observation) bool {
		return o.GetSignalId() == gpusignals.PowerDraw.ID() && o.GetValue() != nil
	}
	if !slices.ContainsFunc(obs, isPowerDraw) {
		return g.CollectAdapter.Build(obs)
	}
	arch, err := g.architecture()
	if err == nil && slices.Contains(averagedPowerDrawArchitectures, arch) {
		return g.CollectAdapter.Build(obs)
	}
	gated := slices.Clone(obs)
	for i, o := range gated {
		if !isPowerDraw(o) {
			continue
		}
		if err != nil {
			gated[i] = observation.NewCollectionErrorObservation(o.GetSignalId(), o.Unit, o.GetSource(), "GPUArchitecture",
				o.GetTarget(), o.GetObservedAt(), &observation.CollectionError{
					Source: gpuinventory.SensorID, Category: observation.CollectionErrorCategoryUnavailable, Detail: err.Error(),
				})
			continue
		}
		gated[i] = observation.NewNoValueObservation(o.GetSignalId(), o.Unit, o.GetSource(), o.GetTarget(), o.GetObservedAt(),
			&observation.NoValue{
				Reason: observation.NoValueReasonNotSupported,
				Detail: fmt.Sprintf("power draw is not a one-second average on architecture %q", arch),
			})
	}
	return g.CollectAdapter.Build(gated)
}

// requiredSignals holds, per category, the catalog signals that checks which
// will run require. A nil requiredSignals requests every declared signal; a
// category present with a nil set requests every signal declared for it.
type requiredSignals map[health.Category]map[string]bool

// requiredFor returns the catalog signals the runnable checks require.
func requiredFor(runnable []*health.CheckDefinition) requiredSignals {
	required := make(requiredSignals)
	for _, check := range runnable {
		set := required[check.Category]
		if set == nil {
			set = make(map[string]bool)
			required[check.Category] = set
		}
		for _, signal := range check.RequiredSignals {
			set[signal] = true
		}
	}
	return required
}

// keeps reports whether a collect signal feeding catalogSignals of category is
// required.
func (r requiredSignals) keeps(category health.Category, catalogSignals []string) bool {
	if r == nil {
		return true
	}
	set, ok := r[category]
	return ok && (set == nil || slices.ContainsFunc(catalogSignals, func(s string) bool { return set[s] }))
}

// declaredAdapter is one adapter's metadata, captured once per run.
type declaredAdapter struct {
	adapter  CollectAdapter
	feeds    map[string][]string
	name     string
	signals  []string
	category health.Category
}

// newCycle starts one collection pass whose sources each get timeout.
func newCycle(timeout time.Duration) (*collect.Cycle, error) {
	return collect.NewCycleWithTimeout(uuid.NewString(), time.Now().UTC(), timeout)
}

// collectSensorsIn resolves registered sensors and adapters, plus the given
// built-ins, collects every source once under cycle, and returns toolkit
// sensors that convert the cached observations. required narrows collection to
// the signals runnable checks require; an adapter or source left with none is
// skipped. Registrations are validated against every declared signal first,
// so whether a registration is valid does not depend on which checks run.
// Each source gets its own timeout budget; the toolkit watchdog then bounds
// only adapter conversion. Registration errors and panics are returned so the
// CLI exits 2 before anything is measured.
func (a *App) collectSensorsIn(cycle *collect.Cycle, builtinSources []collect.Sensor, builtinAdapters []CollectAdapter, required requiredSignals) (sensors []toolkit.Sensor, err error) {
	adapters := slices.Concat(a.collectAdapters, builtinAdapters)
	if len(a.collectSources) == 0 && len(builtinSources) == 0 && len(adapters) == 0 {
		return nil, nil
	}
	if len(adapters) == 0 {
		return nil, fmt.Errorf("collect sensors registered without adapters; register adapters with WithCollectAdapters")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			sensors = nil
			err = fmt.Errorf("collect registration panicked: %v", recovered)
		}
	}()
	declared, err := a.declareAdapters(adapters)
	if err != nil {
		return nil, err
	}
	// Validate against every declared signal on a throwaway runner, so overlap
	// and claim errors do not depend on which checks run.
	if _, err = a.registerSources(collectbridge.NewRunner(cycle), declared, builtinSources, nil); err != nil {
		return nil, err
	}
	runner := collectbridge.NewRunner(cycle)
	bindings, err := a.registerSources(runner, declared, builtinSources, required)
	if err != nil {
		return nil, err
	}
	runner.Collect()
	for _, binding := range bindings {
		sensors = append(sensors, runner.Sensor(binding))
	}
	return sensors, nil
}

// build fails the adapter's output when it strays outside its declared
// category: filtering requested only that category's signals, so a
// measurement of another category would silently miss data its checks need.
func (d declaredAdapter) build(obs []*observation.Observation) []health.Measurement {
	measurements := d.adapter.Build(obs)
	for _, m := range measurements {
		if m != nil && m.Category() != d.category {
			return d.adapter.Errored(fmt.Errorf("collect adapter %q built a %s measurement but declares category %s", d.name, m.Category(), d.category))
		}
	}
	return measurements
}

// declareAdapters captures and validates each adapter's metadata.
func (a *App) declareAdapters(adapters []CollectAdapter) ([]declaredAdapter, error) {
	names := make(map[string]bool)
	declared := make([]declaredAdapter, 0, len(adapters))
	for i, adapter := range adapters {
		if isNilCollectInput(adapter) {
			return nil, fmt.Errorf("registered collect adapter cannot be nil")
		}
		d := declaredAdapter{adapter: adapter, name: adapter.Name(), category: adapter.Category(), feeds: adapter.Signals()}
		if strings.TrimSpace(d.name) == "" || len(d.feeds) == 0 {
			return nil, fmt.Errorf("incomplete collect adapter")
		}
		// Built-ins come last, after every caller adapter.
		var category health.Category
		if i < len(a.collectAdapters) && category.UnmarshalText([]byte(d.name)) == nil {
			return nil, fmt.Errorf("collect adapter name %q is reserved for a built-in domain", d.name)
		}
		if names[d.name] {
			return nil, fmt.Errorf("collect adapter %q is already registered", d.name)
		}
		names[d.name] = true
		d.signals = slices.Sorted(maps.Keys(d.feeds))
		for _, signal := range d.signals {
			if strings.TrimSpace(signal) == "" {
				return nil, fmt.Errorf("adapter %q requests an empty signal", d.name)
			}
		}
		declared = append(declared, d)
	}
	callers, builtins := declared[:len(a.collectAdapters)], declared[len(a.collectAdapters):]
	for _, builtin := range builtins {
		fields := make(map[string]bool)
		for _, feeds := range builtin.feeds {
			for _, field := range feeds {
				fields[field] = true
			}
		}
		for _, caller := range callers {
			if caller.category != builtin.category {
				continue
			}
			for _, signal := range caller.signals {
				for _, field := range caller.feeds[signal] {
					if fields[field] {
						return nil, fmt.Errorf("collect adapter %q feeds %s field %q, which the built-in %q adapter also reports; give the field another name in your measurement type to read the same signal, or claim the signals with WithCollect to replace their built-in source", caller.name, caller.category, field, builtin.name)
					}
				}
			}
		}
	}
	return declared, nil
}

// registerSources binds the adapters and registers caller and built-in
// sources on runner for the signals that required keeps. Claims are validated
// against every declared signal; a claimed or bare caller source left with
// no needed signal is not registered.
func (a *App) registerSources(runner *collectbridge.Runner, adapters []declaredAdapter, builtinSources []collect.Sensor, required requiredSignals) ([]collectbridge.Binding, error) {
	var declared, requested []string
	var bindings []collectbridge.Binding
	for _, d := range adapters {
		var signals []string
		for _, signal := range d.signals {
			declared = append(declared, signal)
			if required.keeps(d.category, d.feeds[signal]) {
				signals = append(signals, signal)
			}
		}
		// Not building it skips its category's runnable checks (SENSOR_MISSING,
		// or NO_APPLICABLE_MEASUREMENT when another sensor supplies the
		// category) rather than failing them for a wiring gap. When a sibling
		// check keeps the adapter built, an unfed schema field still reports
		// UNKNOWN. Tests guard the built-in mappings.
		if len(signals) == 0 {
			continue
		}
		requested = append(requested, signals...)
		bindings = append(bindings, collectbridge.Binding{
			Name: d.name, Signals: signals, Category: d.category,
			Build: d.build, Errored: d.adapter.Errored,
		})
	}
	// Every check here reads declared, not requested, signals, so the outcome
	// does not depend on which checks are runnable.
	claimedBy := make(map[string]int)
	for i, source := range a.collectSources {
		if isNilCollectInput(source.sensor) {
			return nil, fmt.Errorf("registered collect sensor cannot be nil")
		}
		if len(source.claims) == 0 {
			return nil, fmt.Errorf("collect sensor %q claims no signals; pass the signal IDs it supplies to WithCollect", source.sensor.ID())
		}
		if err := checkWholeClaims(source); err != nil {
			return nil, err
		}
		var signals []string
		sensorSignals := source.sensor.Signals()
		for _, id := range source.claims {
			if !slices.ContainsFunc(sensorSignals, func(s collect.Signal) bool { return s != nil && s.ID() == id }) {
				return nil, fmt.Errorf("collect sensor %q does not declare claimed signal %q", source.sensor.ID(), id)
			}
			if !slices.Contains(declared, id) {
				return nil, fmt.Errorf("collect sensor %q claims signal %q, which no adapter requests", source.sensor.ID(), id)
			}
			// Repeating an ID within one call is idempotent.
			if owner, ok := claimedBy[id]; ok && owner != i {
				return nil, fmt.Errorf("signal %q is claimed by both %q and %q; claim each signal from one sensor", id, a.collectSources[owner].sensor.ID(), source.sensor.ID())
			}
			claimedBy[id] = i
			if slices.Contains(requested, id) && !slices.Contains(signals, id) {
				signals = append(signals, id)
			}
		}
		if len(signals) == 0 {
			continue
		}
		if err := runner.Register(source.sensor, signals...); err != nil {
			return nil, err
		}
	}
	unclaimed := slices.DeleteFunc(slices.Clone(requested), func(id string) bool { _, ok := claimedBy[id]; return ok })
	for _, source := range builtinSources {
		owns := false
		for _, s := range source.Signals() {
			if s == nil || !slices.Contains(unclaimed, s.ID()) {
				continue
			}
			if owner := runner.Owner(s.ID()); owner != "" {
				return nil, fmt.Errorf("signal %q is produced by both built-in %q and %q", s.ID(), owner, source.ID())
			}
			owns = true
		}
		// Skipping a fully claimed or unneeded built-in keeps its hardware untouched.
		if !owns {
			continue
		}
		if err := runner.Register(source, unclaimed...); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

// wholeClaimFamilies are signal families whose events must each come from
// one journal read; a split claim lets reads at different times drop fields
// such as an Xid's Fatal or a link transition's state.
var wholeClaimFamilies = []struct {
	name    string
	signals []string
}{
	{name: "Xid", signals: xidsignals.All().IDs()},
	{name: "RDMA link-flap", signals: ibsignals.LinkFlapSignals().IDs()},
}

// checkWholeClaims rejects a claim covering part of a whole-claim family.
// Claims are deduped first so a repeated ID cannot stand in for a missing one.
func checkWholeClaims(source collectSource) error {
	claims := slices.Compact(slices.Sorted(slices.Values(source.claims)))
	for _, family := range wholeClaimFamilies {
		n := 0
		for _, id := range claims {
			if slices.Contains(family.signals, id) {
				n++
			}
		}
		if n > 0 && n < len(family.signals) {
			return fmt.Errorf("collect sensor %q claims only part of the %s signals; claim all of them or none", source.sensor.ID(), family.name)
		}
	}
	return nil
}

// isNilCollectInput also rejects typed nils. Toolkit calls Errored outside any
// recover, so a nil adapter whose Name and Signals ignore the receiver would
// pass registration and crash the run. IsNil panics on non-nilable kinds, hence
// the kind check.
func isNilCollectInput(input any) bool {
	if input == nil {
		return true
	}
	value := reflect.ValueOf(input)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
