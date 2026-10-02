// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"github.com/google/uuid"
)

// CollectAdapter converts validated observations into one catalog domain.
// Name and Signals are captured once per run; adapter names must be unique.
type CollectAdapter interface {
	Name() string
	Signals() []string
	Build([]*observation.Observation) []health.Measurement
	Errored(error) []health.Measurement
}

// WithCollect adds a caller-owned sensor that provides observation facts.
// Adapters registered with [App.WithCollectAdapters] assemble domain
// measurements across all sensors; the two methods may be called in any order.
// Sources and inventory shared by sensors must be constructed by the caller.
// Only requested signals are collected; each sensor must supply at least one.
// Each signal has one producer, so overlapping sensors are a registration error.
// Without signalIDs the sensor supplies every requested signal it declares.
// With signalIDs it supplies exactly those, replacing the built-in collect
// source for them; this succeeds the With*Backend overrides for collect-backed
// domains. Each claimed signal must be declared by the sensor and requested by
// an adapter. A built-in source left with no requested signals never runs, and
// built-in adapters read claimed signals from this sensor.
// Every selected sensor runs; cadence filters evaluation only. Each registered
// collect source has its own timeout budget.
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
// adapters named after their catalog category (for example "cpu"), so those
// names are reserved; a built-in adapter reads its canonical signals from
// whichever registered sensor produces them. Sensor and adapter registration
// order does not matter; both lists are resolved for each run.
// Requested signals without a producer are reported as missing at run time,
// failing only dependent checks rather than causing a registration error.
func (a *App) WithCollectAdapters(adapters ...CollectAdapter) *App {
	a.collectAdapters = append(a.collectAdapters, adapters...)
	return a
}

// builtinCollect returns the stock CLI's collect-backed domains. A domain
// moves here from the legacy sensor list in runCommand when it migrates; its
// With*Backend override keeps the legacy sensor instead.
func (a *App) builtinCollect() ([]collect.Sensor, []CollectAdapter) {
	var sources []collect.Sensor
	var adapters []CollectAdapter
	if a.cpu == nil {
		sources = append(sources, cpusensor.New(nil))
		adapters = append(adapters, collectadapters.CPU())
	}
	return sources, adapters
}

// collectSensors resolves registered sensors and adapters, plus the given
// built-ins, collects every source once under one cycle, and returns toolkit
// sensors that convert the cached observations. Each source gets its own
// timeout budget; the toolkit watchdog then bounds only adapter conversion.
// Registration errors and panics are returned so the CLI exits 2 before
// anything is measured.
func (a *App) collectSensors(timeout time.Duration, builtinSources []collect.Sensor, builtinAdapters []CollectAdapter) (sensors []toolkit.Sensor, err error) {
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
	cycle, err := collect.NewCycleWithTimeout(uuid.NewString(), time.Now().UTC(), timeout)
	if err != nil {
		return nil, err
	}
	runner := collectbridge.NewRunner(cycle)
	var requested []string
	adapterNames := make(map[string]bool)
	var bindings []collectbridge.Binding
	for i, declared := range adapters {
		if isNilCollectInput(declared) {
			return nil, fmt.Errorf("registered collect adapter cannot be nil")
		}
		name, signals := declared.Name(), declared.Signals()
		if strings.TrimSpace(name) == "" || len(signals) == 0 {
			return nil, fmt.Errorf("incomplete collect adapter")
		}
		if adapterNames[name] {
			// Built-ins come last, so a clash at a built-in index is with a caller.
			if i >= len(a.collectAdapters) {
				return nil, fmt.Errorf("collect adapter name %q is reserved for a built-in domain", name)
			}
			return nil, fmt.Errorf("collect adapter %q is already registered", name)
		}
		adapterNames[name] = true
		for _, signal := range signals {
			if strings.TrimSpace(signal) == "" {
				return nil, fmt.Errorf("adapter %q requests an empty signal", name)
			}
			requested = append(requested, signal)
		}
		bindings = append(bindings, collectbridge.Binding{
			Name: name, Signals: slices.Clone(signals),
			Build: declared.Build, Errored: declared.Errored,
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
			signals = source.claims
			declared := source.sensor.Signals()
			for _, id := range source.claims {
				if !slices.ContainsFunc(declared, func(s collect.Signal) bool { return s != nil && s.ID() == id }) {
					return nil, fmt.Errorf("collect sensor %q does not declare claimed signal %q", source.sensor.ID(), id)
				}
				if !slices.Contains(requested, id) {
					return nil, fmt.Errorf("collect sensor %q claims signal %q, which no adapter requests", source.sensor.ID(), id)
				}
				claimed[id] = true
			}
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
		// Skipping a fully claimed built-in keeps its hardware untouched.
		if !owns {
			continue
		}
		if err := runner.Register(source, unclaimed...); err != nil {
			return nil, err
		}
	}
	runner.Collect()
	for _, binding := range bindings {
		sensors = append(sensors, runner.Sensor(binding))
	}
	return sensors, nil
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
