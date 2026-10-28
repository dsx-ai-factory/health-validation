// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
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
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"github.com/google/uuid"
)

// CollectAdapter converts validated observations into one catalog domain.
// Name, Category, and Signals are captured once per run; adapter names must be
// unique. Signals maps each collect signal ID the adapter reads to the catalog
// signals of Category it fills. A run requests a collect signal only when a
// runnable check of Category requires one of the catalog signals it fills
// (catalog signal names are unique only within a category); an adapter left
// with nothing to request is not built. The exception is a check declaring no
// RequiredSignals, which no built-in does: it may read anything, so it gets
// every signal of its Category. Build must return only Category measurements;
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
// every declared signal regardless. Each signal has one producer, so
// overlapping sensors are a registration error.
// Without signalIDs the sensor supplies every requested signal it declares.
// With signalIDs it supplies exactly those, replacing the built-in collect
// source for them; this succeeds the With*Backend overrides for collect-backed
// domains. Each claimed signal must be declared by the sensor and requested by
// an adapter. The Xid signals are claimed all together or not at all. A
// built-in source left with no requested signals never runs, and
// built-in adapters read claimed signals from this sensor.
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
// the adapter list; names must be unique across calls. Built-in domains use
// adapters named after their catalog category (for example "cpu" or "xid"), and a
// caller adapter cannot reuse the name of an active built-in adapter. A domain
// whose built-in is replaced by a With*Backend override or still on its
// legacy sensor has no built-in adapter, so its name is free. A built-in
// adapter reads its mapped signals from whichever registered sensor
// produces them. Sensor and adapter registration order does not matter; both
// lists are resolved for each run.
// Requested signals without a producer are reported as missing at run time,
// failing only dependent checks rather than causing a registration error.
func (a *App) WithCollectAdapters(adapters ...CollectAdapter) *App {
	a.collectAdapters = append(a.collectAdapters, adapters...)
	return a
}

// builtinCollect returns the stock CLI's collect-backed domains. A domain
// moves here from the legacy sensor list in runCommand when it migrates; its
// With*Backend override keeps the legacy sensor instead. The node domain
// reads the host GPU inventory that architecture discovery already read in
// this cycle, so counting GPUs does not enumerate them again.
func (a *App) builtinCollect() ([]collect.Sensor, []CollectAdapter) {
	var sources []collect.Sensor
	var adapters []CollectAdapter
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
		adapters = append(adapters, collectadapters.XID())
	}
	return sources, adapters
}

// requiredSignals holds, per category, the catalog signals that checks which
// will run require. A nil requiredSignals requests every declared signal; a
// category present with a nil set requests every signal declared for it.
type requiredSignals map[health.Category]map[string]bool

// requiredFor returns the catalog signals the runnable checks require. A check
// with no required signals can read any signal of its category.
func requiredFor(runnable []*health.CheckDefinition) requiredSignals {
	required := make(requiredSignals)
	for _, check := range runnable {
		set, seen := required[check.Category]
		if seen && set == nil {
			continue
		}
		if len(check.RequiredSignals) == 0 {
			required[check.Category] = nil
			continue
		}
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
		if names[d.name] {
			// Built-ins come last, so a clash at a built-in index is with a caller.
			if i >= len(a.collectAdapters) {
				return nil, fmt.Errorf("collect adapter name %q is reserved for a built-in domain", d.name)
			}
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
		// check keeps the adapter built, an unfed schema field still fails
		// CRITICAL. Tests guard the built-in mappings.
		if len(signals) == 0 {
			continue
		}
		requested = append(requested, signals...)
		bindings = append(bindings, collectbridge.Binding{
			Name: d.name, Signals: signals, Category: d.category,
			Build: d.build, Errored: d.adapter.Errored,
		})
	}
	// Caller sensors register first so a built-in that overlaps a bare caller
	// sensor can name the fix.
	claimed := make(map[string]bool)
	for _, source := range a.collectSources {
		if isNilCollectInput(source.sensor) {
			return nil, fmt.Errorf("registered collect sensor cannot be nil")
		}
		signals := requested
		if len(source.claims) > 0 {
			// One Xid event's fields must come from one journal read; a split
			// claim lets reads at different times drop fields such as Fatal.
			// Dedupe first so a repeated ID cannot stand in for a missing one.
			xidSignals := observations.MappedSignalsFor(health.Xid)
			xidClaims := slices.Compact(slices.Sorted(slices.Values(source.claims)))
			xidClaims = slices.DeleteFunc(xidClaims, func(id string) bool { return !slices.Contains(xidSignals, id) })
			if len(xidClaims) > 0 && len(xidClaims) < len(xidSignals) {
				return nil, fmt.Errorf("collect sensor %q claims only part of the Xid signals; claim all of them or none", source.sensor.ID())
			}
			signals = nil
			sensorSignals := source.sensor.Signals()
			for _, id := range source.claims {
				if !slices.ContainsFunc(sensorSignals, func(s collect.Signal) bool { return s != nil && s.ID() == id }) {
					return nil, fmt.Errorf("collect sensor %q does not declare claimed signal %q", source.sensor.ID(), id)
				}
				if !slices.Contains(declared, id) {
					return nil, fmt.Errorf("collect sensor %q claims signal %q, which no adapter requests", source.sensor.ID(), id)
				}
				claimed[id] = true
				if slices.Contains(requested, id) {
					signals = append(signals, id)
				}
			}
			if len(signals) == 0 {
				continue
			}
		} else if required != nil && !suppliesAny(source.sensor, requested) {
			// Every signal this sensor would supply belongs to checks that will not run.
			continue
		}
		if err := runner.Register(source.sensor, signals...); err != nil {
			return nil, err
		}
	}
	unclaimed := slices.DeleteFunc(slices.Clone(requested), func(id string) bool { return claimed[id] })
	for _, source := range builtinSources {
		owns := false
		for _, s := range source.Signals() {
			if s == nil || !slices.Contains(unclaimed, s.ID()) {
				continue
			}
			if owner := runner.Owner(s.ID()); owner != "" {
				return nil, fmt.Errorf("signal %q is produced by both %q and built-in %q; pass the signal IDs to WithCollect to replace the built-in source", s.ID(), owner, source.ID())
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

// suppliesAny reports whether sensor declares any of ids.
func suppliesAny(sensor collect.Sensor, ids []string) bool {
	return slices.ContainsFunc(sensor.Signals(), func(s collect.Signal) bool { return s != nil && slices.Contains(ids, s.ID()) })
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
