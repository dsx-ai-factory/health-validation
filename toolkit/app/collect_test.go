// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	linkflapsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/linkflap"
	pcisensor "github.com/dsx-ai-factory/health-validation/collect/sensors/pci"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const customCountSignal = "test.node.custom_count"

// collectSensors is [App.collectSensorsIn] under a fresh cycle. Production
// callers create the cycle themselves so discovery can share it.
func (a *App) collectSensors(timeout time.Duration, builtinSources []collect.Sensor, builtinAdapters []CollectAdapter, required requiredSignals) ([]toolkit.Sensor, error) {
	cycle, err := newCycle(timeout)
	if err != nil {
		return nil, err
	}
	return a.collectSensorsIn(cycle, builtinSources, builtinAdapters, required)
}

// TestMigratedDomainsDefaultToCollectSensors checks built-in wiring and explicit collect-source claims.
func TestMigratedDomainsDefaultToCollectSensors(t *testing.T) {
	builtins := func(a *App) ([]string, []string) {
		a.host = fakeHost{b: spyBackend{called: new(bool)}}
		sources, adapters := a.builtinCollect(nil)
		var sourceIDs, adapterNames []string
		for _, s := range sources {
			sourceIDs = append(sourceIDs, s.ID())
		}
		for _, adapter := range adapters {
			adapterNames = append(adapterNames, adapter.Name())
		}
		return sourceIDs, adapterNames
	}
	tests := []struct {
		app      *App
		name     string
		sources  []string
		adapters []string
	}{
		{name: "default", app: New(), sources: []string{"test.gpu", "pci.sysfs", cpusensor.ID, gpuinventory.SensorID, xidsensor.ID, linkflapsensor.ID}, adapters: []string{"gpu", "pcie", "cpu", "node", "xid", "infiniband", "nvlink"}},
		// A claim replaces a built-in source's signals, not the source list.
		{name: "CPU claimed", app: withCPU(New(), healthyCPU()), sources: []string{"test.gpu", "pci.sysfs", cpusensor.ID, gpuinventory.SensorID, xidsensor.ID, linkflapsensor.ID}, adapters: []string{"gpu", "pcie", "cpu", "node", "xid", "infiniband", "nvlink"}},
	}
	for _, test := range tests {
		sources, adapters := builtins(test.app)
		if !slices.Equal(sources, test.sources) || !slices.Equal(adapters, test.adapters) {
			t.Errorf("%s: built-ins = %v %v, want %v %v", test.name, sources, adapters, test.sources, test.adapters)
		}
	}
}

func TestCollectSkipsUnselectedBridgeSources(t *testing.T) {
	source := &collectAppSensor{}
	a := New().WithCollect(source, claimAll(source)...).WithCollectAdapters(customCountAdapter())
	builtinSources, builtinAdapters := a.builtinCollect(nil)
	sensors, err := a.collectSensors(time.Second, builtinSources, builtinAdapters, requiredSignals{health.Storage: nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 0 || len(source.cycles) != 0 {
		t.Fatalf("unselected bridge sources ran: sensors=%d, cycles=%d", len(sensors), len(source.cycles))
	}
}

func TestCollectionSensorsSelectsBridgeSensorsByAdapterCategory(t *testing.T) {
	a := New().WithCollect(&collectAppSensor{}, claimAll(&collectAppSensor{})...).WithCollectAdapters(customCountAdapter())
	sensors, err := a.collectSensors(time.Second, nil, nil, requiredSignals{health.Node: nil})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := collectionSensors(sensors, map[health.Category]bool{health.Node: true})
	if err != nil || len(selected) != 1 || selected[0].Name() != "custom-count" {
		t.Fatalf("selected=%v err=%v; want the node adapter selected despite its non-domain name", selected, err)
	}
}

func TestNoCallerRegistrationsAddNoBridges(t *testing.T) {
	bridged, err := New().collectSensors(time.Second, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridged) != 0 {
		t.Fatalf("unregistered collect sensors added %d bridges", len(bridged))
	}
}

// collectAppSensor reports a node-scoped custom count of 4 and records each
// cycle it is measured under.
type collectAppSensor struct {
	cycles []*collect.Cycle
}

func (*collectAppSensor) ID() string { return "test.node" }
func (*collectAppSensor) Signals() collect.Signals {
	return collect.Signals{collect.NewSignal(customCountSignal, "1")}
}

func (s *collectAppSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	cycle := collect.NewOp(opts...).Cycle()
	s.cycles = append(s.cycles, cycle)
	return &observation.ObservationBatch{
		CollectionId: cycle.CollectionID(),
		Observations: []*observation.Observation{{
			SignalId:   customCountSignal,
			Source:     s.ID(),
			Unit:       sensorUnit(),
			Target:     observation.NodeTarget(""),
			ObservedAt: timestamppb.Now(),
			Outcome:    &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 4}}},
		}},
	}, nil
}

func sensorUnit() *string { unit := "1"; return &unit }

// customCountMeasurement is a private node-domain measurement for custom checks.
type customCountMeasurement struct {
	failures []health.ErroredMeasurement
	Count    int64 `json:"custom_count"`
}

func (customCountMeasurement) Category() health.Category             { return health.Node }
func (m customCountMeasurement) Errors() []health.ErroredMeasurement { return m.failures }

// collectAppAdapter is a value-receiver adapter built from closures.
type collectAppAdapter struct {
	build   func([]*observation.Observation) []health.Measurement
	errored func(error) []health.Measurement
	signals map[string][]string
	name    string
}

func (a collectAppAdapter) Name() string                 { return a.name }
func (collectAppAdapter) Category() health.Category      { return health.Node }
func (a collectAppAdapter) Signals() map[string][]string { return a.signals }
func (a collectAppAdapter) Build(obs []*observation.Observation) []health.Measurement {
	return a.build(obs)
}
func (a collectAppAdapter) Errored(err error) []health.Measurement { return a.errored(err) }

// lazyNilAdapter only dereferences its receiver in Build and Errored, so a
// typed nil passes Name and Signals and would otherwise crash in Errored.
type lazyNilAdapter struct{ measurements []health.Measurement }

func (*lazyNilAdapter) Name() string              { return "lazy" }
func (*lazyNilAdapter) Category() health.Category { return health.Node }
func (*lazyNilAdapter) Signals() map[string][]string {
	return map[string][]string{customCountSignal: {"custom_count"}}
}

func (a *lazyNilAdapter) Build([]*observation.Observation) []health.Measurement {
	return a.measurements
}
func (a *lazyNilAdapter) Errored(error) []health.Measurement { return a.measurements }

// claimAll lists every signal s declares, for WithCollect.
func claimAll(s collect.Sensor) []string {
	var ids []string
	for _, signal := range s.Signals() {
		if signal != nil {
			ids = append(ids, signal.ID())
		}
	}
	return ids
}

// claimMapped lists the signals s declares that a built-in adapter maps.
func claimMapped(s collect.Sensor) []string {
	return slices.DeleteFunc(claimAll(s), func(id string) bool { return !slices.Contains(observations.MappedSignals(), id) })
}

// customCountAdapter maps the custom count signal to customCountMeasurement.
func customCountAdapter() collectAppAdapter {
	return collectAppAdapter{
		name:    "custom-count",
		signals: map[string][]string{customCountSignal: {"custom_count"}},
		build: func(obs []*observation.Observation) []health.Measurement {
			m := customCountMeasurement{}
			for _, o := range obs {
				if o.GetCollectionError() != nil || o.GetNoValue() != nil {
					m.failures = append(m.failures, health.NewErroredMeasurement(fmt.Errorf("count unavailable"), "custom_count"))
				} else {
					m.Count = o.GetValue().GetIntValue()
				}
			}
			return []health.Measurement{m}
		},
		errored: func(err error) []health.Measurement {
			return []health.Measurement{customCountMeasurement{failures: []health.ErroredMeasurement{health.NewErroredMeasurement(err, "custom_count")}}}
		},
	}
}

func mixedChecks() []health.CheckDefinition {
	return []health.CheckDefinition{
		{
			ID: "mixed_prolog", Name: "Mixed prolog", Category: health.Node,
			RequiredSignals: []string{"custom_count"}, SchedulingCadence: health.Prolog,
			Rules: []health.Rule{{Expression: "custom_count != 4", Severity: health.Critical}},
		},
		{
			ID: "mixed_continuous", Name: "Mixed continuous", Category: health.CPU,
			RequiredSignals: []string{"online_cpu_count"}, Dependencies: []string{"mixed_prolog"},
			Rules: []health.Rule{{Expression: "online_cpu_count != 1", Severity: health.Critical}},
		},
		{
			ID: "mixed_maintenance", Name: "Mixed maintenance", Category: health.Node,
			RequiredSignals: []string{"custom_count"}, SchedulingCadence: health.MaintenanceWindow,
			Rules: []health.Rule{{Expression: "custom_count != 4", Severity: health.Critical}},
		},
	}
}

// checkOutcome is the comparable part of one check verdict or skip.
type checkOutcome struct {
	severity health.Severity
	reason   health.SkipReason
	skipped  bool
}

// mixedOutcomes extracts the mixed-cadence checks' outcomes from v.
func mixedOutcomes(v health.NodeVerdict) map[string]checkOutcome {
	result := make(map[string]checkOutcome)
	for _, c := range append(v.CheckResults, v.SkippedChecks...) {
		if c.CheckID == "mixed_continuous" || c.CheckID == "mixed_prolog" || c.CheckID == "mixed_maintenance" {
			result[c.CheckID] = checkOutcome{severity: c.Severity, reason: c.SkipReason, skipped: c.Skipped}
		}
	}
	return result
}

func TestWithCollectRetainsCadenceAndDependencyEvaluation(t *testing.T) {
	for _, name := range []string{"", "CONTINUOUS", "PROLOG", "MAINTENANCE"} {
		t.Run(name, func(t *testing.T) {
			sensor := &collectAppSensor{}
			cpuCalls := 0
			measurement := cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}
			a := withCleanXid(testAppWithCPU(cpuAppSensor{calls: &cpuCalls, possible: 1, online: 1, numa: 34}))
			a.WithNodeID(testNodeID).WithChecks(mixedChecks()...).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter())
			args := []string{"run"}
			var cadence *health.SchedulingCadence
			if name != "" {
				args = append(args, "--cadence", name)
				value, err := health.SchedulingCadenceFromName(name)
				if err != nil {
					t.Fatal(err)
				}
				cadence = &value
			}
			var stdout, stderr bytes.Buffer
			if code := a.Run(args, &stdout, &stderr); code == exitError {
				t.Fatalf("Run: %s", stderr.String())
			}
			var actual health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &actual); err != nil {
				t.Fatal(err)
			}
			cat, _, err := buildCatalog(nil, mixedChecks(), "GB200", health.DriverVersion{}, health.OperatorInfra)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := cat.EvaluateAllWithOptions(catalog.EvaluationOptions{Cadence: cadence}, measurement, customCountMeasurement{Count: 4})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mixedOutcomes(actual), mixedOutcomes(expected)) {
				t.Fatalf("mixed outcomes = %v; want %v", mixedOutcomes(actual), mixedOutcomes(expected))
			}
			// Only the Prolog and Maintenance checks read the custom count, so a
			// Continuous run leaves its sensor untouched.
			wantCollect := 1
			if name == "CONTINUOUS" {
				wantCollect = 0
			}
			if cpuCalls != 1 || len(sensor.cycles) != wantCollect {
				t.Fatalf("CPU calls=%d custom calls=%d, want 1 and %d", cpuCalls, len(sensor.cycles), wantCollect)
			}
		})
	}
}

// keepOpenHost fails a run that closes the host: the caller owns that close.
type keepOpenHost struct{ hostBackend }

func (keepOpenHost) close() error { panic("Run closed the host backend") }

func TestWithCollectCreatesFreshCycleOnRepeatedRuns(t *testing.T) {
	sensor := &collectAppSensor{}
	a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter())
	a.host = keepOpenHost{a.host}
	for range 2 {
		var stdout, stderr bytes.Buffer
		if code := a.Run([]string{"run"}, &stdout, &stderr); code == exitError {
			t.Fatalf("Run: %s", stderr.String())
		}
	}
	if len(sensor.cycles) != 2 || sensor.cycles[0] == sensor.cycles[1] || sensor.cycles[0].CollectionID() == sensor.cycles[1].CollectionID() {
		t.Fatalf("expected two distinct cycles; got %v", sensor.cycles)
	}
}

func TestWithCollectRegistrationOrderPreservesVerdict(t *testing.T) {
	for _, cadence := range []string{"", "CONTINUOUS", "PROLOG", "MAINTENANCE"} {
		t.Run(cadence, func(t *testing.T) {
			var expected *health.NodeVerdict
			for _, adaptersFirst := range []bool{false, true} {
				source := &collectAppSensor{}
				a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...)
				if adaptersFirst {
					a.WithCollectAdapters(customCountAdapter()).WithCollect(source, claimAll(source)...)
				} else {
					a.WithCollect(source, claimAll(source)...).WithCollectAdapters(customCountAdapter())
				}
				args := []string{"run"}
				if cadence != "" {
					args = append(args, "--cadence", cadence)
				}
				var stdout, stderr bytes.Buffer
				if code := a.Run(args, &stdout, &stderr); code == exitError {
					t.Fatalf("Run: %s", stderr.String())
				}
				var verdict health.NodeVerdict
				if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
					t.Fatal(err)
				}
				wantCalls := 1
				if cadence == "CONTINUOUS" {
					wantCalls = 0
				}
				if len(source.cycles) != wantCalls {
					t.Fatalf("source calls=%d, want %d", len(source.cycles), wantCalls)
				}
				// Each run intentionally has different timestamps and trace IDs.
				verdict.Timestamp, verdict.TraceID = time.Time{}, ""
				for _, results := range [][]health.CheckVerdict{verdict.CheckResults, verdict.SkippedChecks} {
					for i := range results {
						results[i].Timestamp = time.Time{}
						results[i].TraceID, results[i].SpanID = "", ""
					}
				}
				slices.SortFunc(verdict.AffectedComponents, func(a, b health.AffectedComponent) int {
					return strings.Compare(a.Category.String(), b.Category.String())
				})
				if expected == nil {
					expected = &verdict
				} else if !reflect.DeepEqual(verdict, *expected) {
					t.Fatalf("registration order changed verdict:\n got=%+v\nwant=%+v", verdict, *expected)
				}
			}
		})
	}
}

// countedCollectAdapter counts metadata calls to prove they are captured once
// per run.
type countedCollectAdapter struct {
	CollectAdapter
	nameCalls   int
	signalCalls int
}

func (a *countedCollectAdapter) Name() string {
	a.nameCalls++
	return a.CollectAdapter.Name()
}

func (a *countedCollectAdapter) Signals() map[string][]string {
	a.signalCalls++
	return a.CollectAdapter.Signals()
}

func TestWithCollectCapturesAdapterMetadataOncePerRun(t *testing.T) {
	source := &collectAppSensor{}
	adapter := customCountAdapter()
	counted := &countedCollectAdapter{CollectAdapter: &adapter}
	a := New().WithCollect(source, claimAll(source)...).WithCollectAdapters(counted)
	bridged, err := a.collectSensors(time.Second, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if counted.nameCalls != 1 || counted.signalCalls != 1 {
		t.Fatalf("metadata calls: name=%d signals=%d", counted.nameCalls, counted.signalCalls)
	}
	adapter.name = "changed"
	delete(adapter.signals, customCountSignal)
	adapter.signals["changed"] = []string{"custom_count"}
	if bridged[0].Name() != "custom-count" {
		t.Fatalf("adapter name changed after validation: %q", bridged[0].Name())
	}
	measurements, err := bridged[0].Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("measurements=%v", measurements)
	}
	m := measurements[0].(customCountMeasurement)
	if m.Count != 4 || len(m.Errors()) != 0 || len(source.cycles) != 1 {
		t.Fatalf("validated signal selection changed: measurement=%+v calls=%d", m, len(source.cycles))
	}
	adapter.name = "custom-count"
	adapter.signals = customCountAdapter().signals
	if _, err = a.collectSensors(time.Second, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if counted.nameCalls != 2 || counted.signalCalls != 2 {
		t.Fatalf("metadata was not refreshed for the next run: name=%d signals=%d", counted.nameCalls, counted.signalCalls)
	}
}

// metadataCollectAdapter overrides Name or Signals, e.g. to inject panics.
type metadataCollectAdapter struct {
	CollectAdapter
	name    func() string
	signals func() map[string][]string
}

func (a metadataCollectAdapter) Name() string {
	if a.name != nil {
		return a.name()
	}
	return a.CollectAdapter.Name()
}

func (a metadataCollectAdapter) Signals() map[string][]string {
	if a.signals != nil {
		return a.signals()
	}
	return a.CollectAdapter.Signals()
}

// metadataCollectSensor overrides ID or Signals, e.g. to inject panics.
type metadataCollectSensor struct {
	collect.Sensor
	id      func() string
	signals func() collect.Signals
}

func (s metadataCollectSensor) ID() string {
	if s.id != nil {
		return s.id()
	}
	return s.Sensor.ID()
}

func (s metadataCollectSensor) Signals() collect.Signals {
	if s.signals != nil {
		return s.signals()
	}
	return s.Sensor.Signals()
}

func TestWithCollectReportsRegistrationPanics(t *testing.T) {
	for _, method := range []string{"adapter Name", "adapter Signals", "sensor ID", "sensor Signals"} {
		t.Run(method, func(t *testing.T) {
			source := &collectAppSensor{}
			sensor := metadataCollectSensor{Sensor: source}
			adapter := metadataCollectAdapter{CollectAdapter: customCountAdapter()}
			switch method {
			case "adapter Name":
				adapter.name = func() string { panic(method) }
			case "adapter Signals":
				adapter.signals = func() map[string][]string { panic(method) }
			case "sensor ID":
				sensor.id = func() string { panic(method) }
			case "sensor Signals":
				sensor.signals = func() collect.Signals { panic(method) }
			}
			a := cadenceRunApp().WithNodeID(testNodeID).WithCollect(sensor, customCountSignal).WithCollectAdapters(adapter)
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError || stdout.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "register collect sensors: collect registration panicked: "+method) {
				t.Fatalf("registration panic was not reported: %q", stderr.String())
			}
			if len(source.cycles) != 0 || *a.host.(fakeHost).b.called {
				t.Fatal("registration panic allowed measurement")
			}
		})
	}
}

func TestWithCollectRejectsInvalidRegistrationsBeforeMeasuring(t *testing.T) {
	for _, name := range []string{"nil sensor", "typed nil sensor", "nil signal", "nil adapter", "typed nil adapter", "lazy typed nil adapter", "incomplete adapter", "unowned signal", "duplicate producer", "duplicate adapter", "duplicate adapter across calls", "built-in adapter name", "built-in xid adapter name"} {
		t.Run(name, func(t *testing.T) {
			sensor := &collectAppSensor{}
			var otherCycles []*collect.Cycle
			var selected []string
			a := cadenceRunApp().WithNodeID(testNodeID)
			switch name {
			case "nil sensor":
				a.WithCollect(nil).WithCollectAdapters(customCountAdapter())
			case "typed nil sensor":
				var source *collectAppSensor
				a.WithCollect(source, claimAll(source)...).WithCollectAdapters(customCountAdapter())
			case "nil signal":
				a.WithCollect(metadataCollectSensor{Sensor: sensor, signals: func() collect.Signals {
					return collect.Signals{collect.NewSignal(customCountSignal, "1"), nil}
				}}, customCountSignal).WithCollectAdapters(customCountAdapter())
			case "nil adapter":
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(nil)
			case "typed nil adapter":
				var adapter *collectAppAdapter
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "lazy typed nil adapter":
				var adapter *lazyNilAdapter
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "incomplete adapter":
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(collectAppAdapter{})
			case "unowned signal":
				adapter := customCountAdapter()
				adapter.signals = map[string][]string{"unknown": {"custom_count"}}
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "duplicate producer":
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter()).WithCollect(metadataCollectSensor{
					Sensor: &collectAppSensor{}, id: func() string { return "test.node.other" },
				}, customCountSignal)
			case "duplicate adapter":
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter(), customCountAdapter())
			case "built-in adapter name":
				adapter := customCountAdapter()
				adapter.name = "cpu"
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "built-in xid adapter name":
				adapter := customCountAdapter()
				adapter.name = "xid"
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "duplicate adapter across calls":
				adapter := customCountAdapter()
				adapter.signals["test.empty"] = []string{"custom_count"}
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter).WithCollect(&emptyAppSensor{cycles: &otherCycles, selected: &selected}, claimAll(&emptyAppSensor{cycles: &otherCycles, selected: &selected})...).WithCollectAdapters(adapter)
			}
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if strings.Contains(name, "nil adapter") && !strings.Contains(stderr.String(), "registered collect adapter cannot be nil") {
				t.Fatalf("nil adapter did not cause setup failure: %q", stderr.String())
			}
			if strings.Contains(name, "nil sensor") && !strings.Contains(stderr.String(), "registered collect sensor cannot be nil") {
				t.Fatalf("nil sensor did not cause setup failure: %q", stderr.String())
			}
			if name == "nil signal" && !strings.Contains(stderr.String(), "declares a nil signal") {
				t.Fatalf("nil signal did not cause setup failure: %q", stderr.String())
			}
			if name == "unowned signal" && !strings.Contains(stderr.String(), "which no adapter requests") {
				t.Fatalf("sensor without requested signals did not cause setup failure: %q", stderr.String())
			}
			if name == "duplicate producer" && !strings.Contains(stderr.String(), "claim each signal from one sensor") {
				t.Fatalf("overlapping producers were not rejected: %q", stderr.String())
			}
			if strings.HasPrefix(name, "duplicate adapter") && !strings.Contains(stderr.String(), `collect adapter "custom-count" is already registered`) {
				t.Fatalf("duplicate adapter did not cause setup failure: %q", stderr.String())
			}
			if name == "built-in adapter name" && !strings.Contains(stderr.String(), `collect adapter name "cpu" is reserved for a built-in domain`) {
				t.Fatalf("built-in adapter name was not reserved: %q", stderr.String())
			}
			if name == "built-in xid adapter name" && !strings.Contains(stderr.String(), `collect adapter name "xid" is reserved for a built-in domain`) {
				t.Fatalf("built-in xid adapter name was not reserved: %q", stderr.String())
			}
			if len(sensor.cycles) != 0 || len(otherCycles) != 0 {
				t.Fatal("invalid registration was measured")
			}
		})
	}
}

// Every category name is reserved, so a domain moving to a built-in adapter
// later cannot collide with a caller adapter that took its name.
func TestWithCollectReservesEveryCategoryAdapterName(t *testing.T) {
	for _, test := range []struct {
		configure func(*App)
		name      string
	}{
		{name: "gpu"},        // built-in active
		{name: "nvlink"},     // built-in active
		{name: "infiniband"}, // built-in active
	} {
		t.Run(test.name, func(t *testing.T) {
			sensor := &collectAppSensor{}
			adapter := customCountAdapter()
			adapter.name = test.name
			a := cadenceRunApp().WithNodeID(testNodeID).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			if test.configure != nil {
				test.configure(a)
			}
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError || stdout.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if want := fmt.Sprintf("collect adapter name %q is reserved for a built-in domain", test.name); !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr=%q, want %q", stderr.String(), want)
			}
			if len(sensor.cycles) != 0 {
				t.Fatal("invalid registration was measured")
			}
		})
	}
}

// emptyAppSensor succeeds with no observations and records its signal selection.
type emptyAppSensor struct {
	cycles   *[]*collect.Cycle
	selected *[]string
}

func (*emptyAppSensor) ID() string { return "test.empty" }
func (*emptyAppSensor) Signals() collect.Signals {
	return collect.Signals{collect.NewSignal("test.empty", ""), collect.NewSignal("test.unused", "")}
}

func (s *emptyAppSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	signals, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	*s.cycles = append(*s.cycles, op.Cycle())
	*s.selected = signals.IDs()
	return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}, nil
}

func TestWithCollectJoinsProducersAndSelectsRequestedSignals(t *testing.T) {
	sensor := &collectAppSensor{}
	var cycles []*collect.Cycle
	var selected []string
	adapter := customCountAdapter()
	adapter.signals["test.empty"] = []string{"custom_count"}
	a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...).
		WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter).WithCollect(&emptyAppSensor{cycles: &cycles, selected: &selected}, "test.empty")
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code == exitError {
		t.Fatalf("Run: %s", stderr.String())
	}
	if len(sensor.cycles) != 1 || len(cycles) != 1 || sensor.cycles[0] != cycles[0] {
		t.Fatal("producers did not share one cycle")
	}
	if !reflect.DeepEqual(selected, []string{"test.empty"}) {
		t.Fatalf("selected=%v", selected)
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	if mixedOutcomes(verdict)["mixed_prolog"].severity != health.Pass {
		t.Fatalf("joined adapter failed: %s", stdout.String())
	}
}

func TestWithCollectReportsMissingProducer(t *testing.T) {
	sensor := &collectAppSensor{}
	adapter := customCountAdapter()
	adapter.signals["test.missing"] = []string{"missing_count"}
	seen := make(chan observation.CollectionErrorCategory, 1)
	build := adapter.build
	adapter.build = func(obs []*observation.Observation) []health.Measurement {
		for _, o := range obs {
			if o.SignalId == "test.missing" {
				seen <- o.GetCollectionError().Category
			}
		}
		return build(obs)
	}
	missingCheck := health.CheckDefinition{
		ID: "missing_count", Name: "Missing count", Category: health.Node, RequiredSignals: []string{"missing_count"},
		Rules: []health.Rule{{Expression: "missing_count != 0", Severity: health.Critical}},
	}
	a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(missingCheck).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code == exitError {
		t.Fatalf("Run: %s", stderr.String())
	}
	select {
	case category := <-seen:
		if category != observation.CollectionErrorCategoryNotConfigured {
			t.Fatalf("category=%v", category)
		}
	default:
		t.Fatal("missing producer was silently ignored")
	}
}

// partialCountMeasurement has one field fed by a producer and one left missing.
type partialCountMeasurement struct {
	failures     []health.ErroredMeasurement
	Count        int64 `json:"custom_count"`
	MissingCount int64 `json:"missing_count"`
}

func (partialCountMeasurement) Category() health.Category             { return health.Node }
func (m partialCountMeasurement) Errors() []health.ErroredMeasurement { return m.failures }

func TestWithCollectMissingProducerFailsOnlyDependentChecks(t *testing.T) {
	source := &collectAppSensor{}
	adapter := customCountAdapter()
	adapter.signals["test.missing"] = []string{"missing_count"}
	adapter.build = func(obs []*observation.Observation) []health.Measurement {
		m := partialCountMeasurement{}
		for _, o := range obs {
			if o.GetSignalId() == "test.missing" {
				if o.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryNotConfigured {
					t.Errorf("missing producer outcome=%v", o)
				}
				m.failures = append(m.failures, health.NewErroredMeasurement(fmt.Errorf("missing count unavailable"), "missing_count"))
			} else {
				m.Count = o.GetValue().GetIntValue()
			}
		}
		return []health.Measurement{m}
	}
	a := New().WithCollectAdapters(adapter).WithCollect(source, claimAll(source)...)
	sensors, err := a.collectSensors(time.Second, nil, nil, nil)
	if err != nil {
		t.Fatalf("missing producer became a registration error: %v", err)
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	checks := []health.CheckDefinition{
		{
			ID: "available_count", Name: "Available count", Category: health.Node, RequiredSignals: []string{"custom_count"},
			Rules: []health.Rule{{Expression: "custom_count != 4", Severity: health.Critical}},
		},
		{
			ID: "missing_count", Name: "Missing count", Category: health.Node, RequiredSignals: []string{"missing_count"},
			Rules: []health.Rule{{Expression: "missing_count != 0", Severity: health.Critical}},
		},
		cpu.CPUCount(),
	}
	for _, check := range checks {
		if err = cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	sensors = append(sensors, measurementSensor{name: "cpu", ms: []health.Measurement{cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1}}})
	verdict, err := toolkit.Collect(cat, sensors...)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.CheckResults) != 3 || len(verdict.SkippedChecks) != 0 || len(source.cycles) != 1 {
		t.Fatalf("unexpected coverage or calls: verdict=%+v calls=%d", verdict, len(source.cycles))
	}
	for _, check := range verdict.CheckResults {
		want := health.Pass
		if check.CheckID == "missing_count" {
			want = health.Unknown
		}
		if check.Severity != want {
			t.Fatalf("check %s severity=%v, want %v", check.CheckID, check.Severity, want)
		}
		if check.CheckID == "missing_count" && !strings.Contains(strings.Join(check.Messages, " "), "missing count unavailable") {
			t.Fatalf("missing input did not cause the failure: %+v", check)
		}
	}
}

func TestWithCollectAdaptersWithoutSensorsReportMissingInputs(t *testing.T) {
	a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...).WithCollectAdapters(customCountAdapter())
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("exit=%d, want %d; stderr=%q", code, exitUnhealthy, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	if outcome, ok := mixedOutcomes(verdict)["mixed_prolog"]; !ok || outcome.skipped || outcome.severity != health.Unknown {
		t.Fatalf("adapter without sources did not report missing inputs: %+v", verdict)
	}
}

func TestWithCollectCombinesGPUObservationsFromTwoSensors(t *testing.T) {
	ids := []string{"GPU-1", "GPU-2"}
	power := &collectHardwareSensor{id: "test.power", gpuIDs: ids, signals: collect.Signals{
		gpusignals.PowerDraw, gpusignals.PowerLimitRequested, gpusignals.PowerLimitEnforced,
	}}
	state := &collectHardwareSensor{id: "test.state", gpuIDs: ids, signals: collect.Signals{
		gpusignals.PerformanceState, gpusignals.HardwareSlowdownActive,
		gpusignals.HardwareThermalSlowdownActive, gpusignals.HardwarePowerBrakeSlowdownActive,
		gpusignals.ProcessCount, gpusignals.ProcessCmdline,
	}}
	a := New().WithCollect(power, claimAll(power)...).WithCollect(state, claimAll(state)...)
	sensors, err := a.collectSensors(time.Second, nil, []CollectAdapter{collectadapters.GPU()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 1 {
		t.Fatalf("bridged sensors=%d, want 1", len(sensors))
	}
	measurements, err := sensors[0].Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != len(ids) {
		t.Fatalf("measurements=%v, want one per GPU", measurements)
	}
	seen := make(map[string]bool)
	for _, measurement := range measurements {
		m := measurement.(gpu.GPUMeasurement)
		if seen[m.GPUID] || !slices.Contains(ids, m.GPUID) {
			t.Fatalf("unexpected or duplicate GPU measurement: %+v", m)
		}
		seen[m.GPUID] = true
		if m.PowerLimitW != 700 || m.EnforcedPowerLimitW != 700 || m.GPUPstate != 0 || m.HWSlowdown || m.HWThermalSlowdown || m.HWPowerBrakeSlowdown {
			t.Fatalf("GPU observations were not combined: %+v", m)
		}
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("H100"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{gpu.GPUPowerLimit(), gpu.GPUPowerState(), gpu.GPUSensorThrottle()} {
		if err = cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := toolkit.Collect(cat, sensors...)
	if err != nil {
		t.Fatal(err)
	}
	if power.calls != 1 || state.calls != 1 || len(power.cycles) != 1 || len(state.cycles) != 1 || power.cycles[0] != state.cycles[0] {
		t.Fatalf("sources were not collected once on a shared cycle: power=%+v state=%+v", power, state)
	}
	if len(verdict.CheckResults) != 6 || len(verdict.SkippedChecks) != 0 {
		t.Fatalf("combined measurements lost checks: %+v", verdict)
	}
	for _, check := range verdict.CheckResults {
		if check.Severity != health.Pass {
			t.Fatalf("combined measurement check %s failed: %+v", check.CheckID, check)
		}
	}
}

var (
	_ CollectAdapter = collectadapters.GPU()
	_ CollectAdapter = collectadapters.NVLink()
)

// collectHardwareSensor emits healthy canonical GPU observations for each of
// gpuIDs, restricted to the selected signals.
type collectHardwareSensor struct {
	values  map[string]*observation.Value // per-signal overrides
	id      string
	cycles  []*collect.Cycle
	signals collect.Signals
	gpuIDs  []string
	calls   int
}

func (s *collectHardwareSensor) ID() string {
	if s.id != "" {
		return s.id
	}
	return "test.hardware"
}

func (s *collectHardwareSensor) Signals() collect.Signals {
	if s.signals != nil {
		return s.signals
	}
	return gpusignals.All()
}

func (s *collectHardwareSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	s.calls++
	op := collect.NewOp(opts...)
	s.cycles = append(s.cycles, op.Cycle())
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	gpuIDs := s.gpuIDs
	if len(gpuIDs) == 0 {
		gpuIDs = []string{"GPU-1"}
	}
	for _, gpuID := range gpuIDs {
		for _, signal := range selected {
			value := &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: false}}
			switch signal.ID() {
			case gpusignals.PowerDraw.ID(), gpusignals.PowerLimitRequested.ID(), gpusignals.PowerLimitEnforced.ID():
				value.Kind = &observation.Value_DoubleValue{DoubleValue: 700}
			case gpusignals.PerformanceState.ID():
				value.Kind = &observation.Value_IntValue{IntValue: 0}
			case gpusignals.FabricManagerStatus.ID():
				value.Kind = &observation.Value_IntValue{IntValue: gpusignals.FabricManagerStatusSuccess}
			case gpusignals.NVLinkState.ID():
				value.Kind = &observation.Value_UintValue{UintValue: 1}
			case gpusignals.NVLinkLinkCount.ID():
				value.Kind = &observation.Value_UintValue{UintValue: 18}
			case gpusignals.NVLinkSpeedCommon.ID(), gpusignals.NVLinkEndpointSpeed.ID():
				value.Kind = &observation.Value_UintValue{UintValue: 53125}
			case gpusignals.NVLinkP2PStatus.ID(), gpusignals.ProcessCount.ID():
				value.Kind = &observation.Value_UintValue{UintValue: 0}
			case gpusignals.ProcessCmdline.ID():
				// An idle GPU, as collect's NVML sensor reports one.
				batch.Observations = append(batch.Observations, observation.NewNoValueObservation(signal.ID(), signal.Unit(), s.ID(),
					observation.GPUTarget(gpuID), timestamppb.Now(), &observation.NoValue{Reason: observation.NoValueReasonNoTargets}))
				continue
			}
			if slices.Contains(gpusignals.EndpointCapabilitySignals().IDs(), signal.ID()) {
				value.Kind = &observation.Value_UintValue{UintValue: 1}
			}
			if override, ok := s.values[signal.ID()]; ok {
				value = override
			}
			target := observation.GPUTarget(gpuID)
			switch {
			case slices.Contains(gpusignals.EndpointSignals().IDs(), signal.ID()):
				target = observation.NVLinkEndpointTarget(gpuID, 0)
			case slices.Contains(gpusignals.PairSignals().IDs(), signal.ID()):
				target = observation.GPUPairTarget(gpuID, "GPU-peer")
			}
			batch.Observations = append(batch.Observations, &observation.Observation{
				SignalId: signal.ID(), Source: s.ID(), Target: target,
				ObservedAt: timestamppb.Now(), Unit: signal.Unit(),
				Outcome: &observation.Observation_Value{Value: value},
			})
		}
	}
	return batch, nil
}

func TestWithCollectWrongScalarFailsOnlyDependentChecks(t *testing.T) {
	wrong := &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: true}}
	gb200Limit := &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: 1200}}
	source := &collectHardwareSensor{values: map[string]*observation.Value{
		gpusignals.PerformanceState.ID():    wrong,
		gpusignals.FabricManagerStatus.ID(): wrong,
		gpusignals.PowerLimitRequested.ID(): gb200Limit,
		gpusignals.PowerLimitEnforced.ID():  gb200Limit,
	}}
	sensors, err := New().WithCollect(source, claimMapped(source)...).collectSensors(time.Second, nil, []CollectAdapter{collectadapters.GPU(), collectadapters.NVLink()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Unmapped catalog fields always fail, so assert on the mapped ones only.
	var failed []string
	for _, sensor := range sensors {
		measurements, measureErr := sensor.Measure(context.Background())
		if measureErr != nil {
			t.Fatal(measureErr)
		}
		for _, m := range measurements {
			for _, e := range m.Errors() {
				failed = append(failed, e.FailedSignals()...)
			}
		}
	}
	for _, signal := range []string{gpu.SignalGPUPstate, nvlink.SignalFabricRegistrationIncomplete, nvlink.SignalFabricRegistrationFailed} {
		if !slices.Contains(failed, signal) {
			t.Errorf("wrong scalar did not fail %s: %v", signal, failed)
		}
	}
	for _, signal := range []string{gpu.SignalPowerLimitW, gpu.SignalHWSlowdown, nvlink.SignalFabricRouteUnhealthy} {
		if slices.Contains(failed, signal) {
			t.Errorf("valid sibling %s failed: %v", signal, failed)
		}
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{
		gpu.GPUPowerState(), gpu.GPUPowerLimit(), gpu.GPUSensorThrottle(),
		nvlink.NVLinkFabric(), nvlink.NVLinkFabricHealth(),
	} {
		if err = cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := toolkit.Collect(cat, sensors...)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || len(verdict.CheckResults) != 5 || len(verdict.SkippedChecks) != 0 {
		t.Fatalf("calls=%d verdict=%+v", source.calls, verdict)
	}
	failing := map[string]string{
		"gpu_power_state": gpusignals.PerformanceState.ID(),
		"nvlink_fabric":   gpusignals.FabricManagerStatus.ID(),
	}
	for _, check := range verdict.CheckResults {
		signal, wantFail := failing[check.CheckID]
		if !wantFail {
			if check.Severity != health.Pass {
				t.Errorf("unrelated check %s=%+v", check.CheckID, check)
			}
			continue
		}
		if check.Severity == health.Pass || !strings.Contains(strings.Join(check.Messages, " "), signal) {
			t.Errorf("check %s did not fail on its wrong scalar: %+v", check.CheckID, check)
		}
	}
}

// slotSensor reports one of collectHardwareSensor's GPUs only by a
// source-local ID, as DCGM does when that GPU's UUID read fails.
type slotSensor struct {
	*collectHardwareSensor
	uuid string
}

func (s slotSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	batch, err := s.collectHardwareSensor.Measure(ctx, opts...)
	for _, o := range batch.GetObservations() {
		target := o.GetTarget()
		for _, ref := range []*observation.GPURef{target.GetGpu(), target.GetNvLinkEndpoint().GetGpu(), target.GetGpuPair().GetSourceGpu()} {
			if ref.GetUuid() == s.uuid {
				ref.Uuid, ref.SourceLocalId = "", "slot-2"
			}
		}
	}
	return batch, err
}

// A GPU without a UUID fails its own GPU and NVLink checks; the GPU with one
// still evaluates, rather than the whole batch failing.
func TestWithCollectFailsOnlyTheGPUWithoutAUUID(t *testing.T) {
	source := slotSensor{collectHardwareSensor: &collectHardwareSensor{gpuIDs: []string{"GPU-1", "GPU-2"}}, uuid: "GPU-2"}
	sensors, err := New().WithCollect(source, claimMapped(source)...).collectSensors(time.Second, nil, []CollectAdapter{collectadapters.GPU(), collectadapters.NVLink()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{gpu.GPUPowerState(), nvlink.NVLinkFabric()} {
		if err = cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := toolkit.Collect(cat, sensors...)
	if err != nil {
		t.Fatal(err)
	}
	severities := map[string][]health.Severity{}
	for _, result := range verdict.CheckResults {
		severities[result.CheckID] = append(severities[result.CheckID], result.Severity)
	}
	for _, check := range []string{"gpu_power_state", "nvlink_fabric"} {
		got := severities[check]
		slices.Sort(got)
		if !slices.Equal(got, []health.Severity{health.Pass, health.Unknown}) {
			t.Errorf("%s severities = %v, want one PASS and one UNKNOWN", check, got)
		}
	}
}

// eccMeasurement is a consumer's per-GPU measurement for GPUAdapter. It also
// reads the performance state, which built-in checks read as gpu_pstate, under
// its own field name.
type eccMeasurement struct {
	GPUID string `json:"gpu_id"`
	observations.Entity
	DoubleBit int64 `json:"ecc_double_bit"`
	Pstate    int64 `json:"pstate"`
}

func (eccMeasurement) Category() health.Category { return health.GPU }
func (m eccMeasurement) EntityID() string        { return m.GPUID }

// eccHost adds a built-in source of the ECC signal, as the linux NVML sensor
// declares, to fakeHost's DCGM-shaped GPU source.
type eccHost struct {
	ecc collect.Sensor
	fakeHost
}

func (h eccHost) gpuSensors() []collect.Sensor { return append(h.fakeHost.gpuSensors(), h.ecc) }

// A consumer check registered with only a GPUAdapter runs next to the stock GPU
// checks: the built-in sources supply both through runnable-signal selection,
// and the performance state feeds the built-in and the consumer adapter alike.
func TestGPUAdapterRunsAlongsideBuiltinGPUChecks(t *testing.T) {
	ecc := gpusignals.ECCDoubleBitVolatileTotal
	gpus := []string{"GPU-0", "GPU-1", "GPU-2", "GPU-3"}
	eccSource := &collectHardwareSensor{
		id: "test.nvml", signals: collect.Signals{ecc}, gpuIDs: gpus,
		values: map[string]*observation.Value{ecc.ID(): {Kind: &observation.Value_UintValue{UintValue: 0}}},
	}
	adapter, err := GPUAdapter[eccMeasurement]("ecc", map[string]string{
		ecc.ID():                         "ecc_double_bit",
		gpusignals.PerformanceState.ID(): "pstate",
	})
	if err != nil {
		t.Fatal(err)
	}
	a := cadenceRunApp().WithNodeID(testNodeID).WithCollectAdapters(adapter).WithChecks(health.CheckDefinition{
		ID: "ecc_at_full_performance", Name: "ECC at full performance", Category: health.GPU,
		RequiredSignals: []string{"ecc_double_bit", "pstate"},
		Rules:           []health.Rule{{Expression: "ecc_double_bit > 0 || pstate != 0", Severity: health.Critical}},
	})
	a.host = eccHost{fakeHost: a.host.(fakeHost), ecc: eccSource}
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code == exitError {
		t.Fatalf("Run: %s", stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	perGPU := map[string]int{}
	for _, v := range verdict.CheckResults {
		if v.CheckID == "ecc_at_full_performance" && v.Severity != health.Pass {
			t.Errorf("consumer check %+v, want PASS", v)
		}
		perGPU[v.CheckID]++
	}
	skipped := map[string]health.SkipReason{}
	for _, v := range verdict.SkippedChecks {
		skipped[v.CheckID] = v.SkipReason
	}
	for _, check := range []string{"ecc_at_full_performance", "gpu_power_state", "gpu_sensor_throttle"} {
		if perGPU[check] != len(gpus) {
			t.Errorf("%s verdicts = %d, want one per GPU (skip reason %q)", check, perGPU[check], skipped[check])
		}
	}
	if eccSource.calls != 1 || !*a.host.(eccHost).b.called {
		t.Fatalf("ECC source calls=%d, DCGM-shaped source called=%v", eccSource.calls, *a.host.(eccHost).b.called)
	}
}

// Reading a built-in signal into a field the built-in adapter reports is
// rejected, and the error names the rename that works.
func TestGPUAdapterRejectsBuiltinFieldName(t *testing.T) {
	adapter, err := GPUAdapter[gpuPstateCopy]("pstate-copy", map[string]string{gpusignals.PerformanceState.ID(): gpu.SignalGPUPstate})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := cadenceRunApp().WithNodeID(testNodeID).WithCollectAdapters(adapter).Run([]string{"run"}, &stdout, &stderr); code != exitError {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if want := `give the field another name in your measurement type to read the same signal`; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr=%q, want %q", stderr.String(), want)
	}
}

type gpuPstateCopy struct {
	GPUID string `json:"gpu_id"`
	observations.Entity
	Pstate int64 `json:"gpu_pstate"`
}

func (gpuPstateCopy) Category() health.Category { return health.GPU }
func (m gpuPstateCopy) EntityID() string        { return m.GPUID }

func TestGPUAdapterRejectsUnusableType(t *testing.T) {
	if _, err := GPUAdapter[eccMeasurement]("ecc", map[string]string{"s": "typo"}); err == nil || !strings.Contains(err.Error(), `GPU adapter "ecc"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithCollectRegistersReusableDomainAdapters(t *testing.T) {
	source := &collectHardwareSensor{}
	a := New().WithCollect(source, claimMapped(source)...)
	sensors, err := a.collectSensors(time.Second, nil, []CollectAdapter{collectadapters.GPU(), collectadapters.NVLink()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 2 {
		t.Fatalf("bridged sensors=%d, want 2", len(sensors))
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{gpu.GPUPowerState(), nvlink.NVLinkFabric()} {
		if err = cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := toolkit.Collect(cat, sensors...)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || len(verdict.CheckResults) != 2 {
		t.Fatalf("calls=%d, verdict=%+v", source.calls, verdict)
	}
	for _, check := range verdict.CheckResults {
		if check.Severity != health.Pass {
			t.Fatalf("check %s=%+v", check.CheckID, check)
		}
	}
}

// claimSignals returns the signals sourceAdapter requests.
func claimSignals() collect.Signals {
	return collect.Signals{collect.NewSignal("test.a", ""), collect.NewSignal("test.b", "")}
}

// sourceAdapter requests claimSignals and records each observation's producer
// as "signal/source".
func sourceAdapter(produced *[]string) collectAppAdapter {
	return collectAppAdapter{
		name: "sources", signals: map[string][]string{"test.a": {"a"}, "test.b": {"b"}},
		build: func(obs []*observation.Observation) []health.Measurement {
			for _, o := range obs {
				*produced = append(*produced, o.GetSignalId()+"/"+o.GetSource())
			}
			return nil
		},
		errored: func(error) []health.Measurement { return nil },
	}
}

func TestWithCollectClaimsReplaceBuiltinSources(t *testing.T) {
	for _, test := range []struct {
		name         string
		claims       []string
		want         []string
		builtinCalls int
	}{
		{name: "no caller", want: []string{"test.a/test.builtin", "test.b/test.builtin"}, builtinCalls: 1},
		{name: "partial claim", claims: []string{"test.a"}, want: []string{"test.a/test.caller", "test.b/test.builtin"}, builtinCalls: 1},
		{name: "full claim", claims: []string{"test.a", "test.b"}, want: []string{"test.a/test.caller", "test.b/test.caller"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builtin := &collectHardwareSensor{id: "test.builtin", signals: claimSignals()}
			caller := &collectHardwareSensor{id: "test.caller", signals: claimSignals()}
			a := New()
			if test.claims != nil {
				a.WithCollect(caller, test.claims...)
			}
			var produced []string
			sensors, err := a.collectSensors(time.Second, []collect.Sensor{builtin}, []CollectAdapter{sourceAdapter(&produced)}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = sensors[0].Measure(context.Background()); err != nil {
				t.Fatal(err)
			}
			slices.Sort(produced)
			if !reflect.DeepEqual(produced, test.want) || builtin.calls != test.builtinCalls {
				t.Fatalf("produced=%v builtin calls=%d; want %v and %d", produced, builtin.calls, test.want, test.builtinCalls)
			}
		})
	}
}

func TestWithCollectRejectsInvalidClaims(t *testing.T) {
	for _, name := range []string{"bare sensor", "undeclared claim", "unrequested claim", "duplicate claim"} {
		t.Run(name, func(t *testing.T) {
			builtin := &collectHardwareSensor{id: "test.builtin", signals: claimSignals()}
			caller := &collectHardwareSensor{id: "test.caller", signals: claimSignals()}
			other := &collectHardwareSensor{id: "test.other", signals: claimSignals()}
			a := New()
			var want string
			switch name {
			case "bare sensor":
				a.WithCollect(caller)
				want = `collect sensor "test.caller" claims no signals; pass the signal IDs it supplies to WithCollect`
			case "undeclared claim":
				caller.signals = claimSignals()[:1]
				a.WithCollect(caller, "test.b")
				want = `collect sensor "test.caller" does not declare claimed signal "test.b"`
			case "unrequested claim":
				caller.signals = append(claimSignals(), collect.NewSignal("test.c", ""))
				a.WithCollect(caller, "test.c")
				want = `collect sensor "test.caller" claims signal "test.c", which no adapter requests`
			case "duplicate claim":
				a.WithCollect(caller, "test.a", "test.a").WithCollect(other, "test.a")
				want = `signal "test.a" is claimed by both "test.caller" and "test.other"; claim each signal from one sensor`
			}
			var produced []string
			_, err := a.collectSensors(time.Second, []collect.Sensor{builtin}, []CollectAdapter{sourceAdapter(&produced)}, nil)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err=%v, want %q", err, want)
			}
			if builtin.calls+caller.calls+other.calls != 0 {
				t.Fatal("invalid claim was measured")
			}
		})
	}
}

// recordingSensor declares signals, returns no observations, and records the
// signals each call selected.
type recordingSensor struct {
	id       string
	signals  collect.Signals
	selected [][]string
}

func (s *recordingSensor) ID() string               { return s.id }
func (s *recordingSensor) Signals() collect.Signals { return s.signals }

func (s *recordingSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.signals)
	if err != nil {
		return nil, err
	}
	s.selected = append(s.selected, selected.IDs())
	return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}, nil
}

func TestCollectSensorsRequestsOnlyRunnableCheckSignals(t *testing.T) {
	for _, test := range []struct {
		name     string
		runnable []health.CheckDefinition
		want     [][]string
		bridged  int
	}{
		{name: "every check runs", runnable: []health.CheckDefinition{cpu.CPUCount(), cpu.NUMANodeCount()}, want: [][]string{{
			cpusignals.PossibleCount.ID(), cpusignals.OnlineCount.ID(), cpusignals.NUMANodeOnlineCount.ID(),
		}}, bridged: 1},
		{name: "one check runs", runnable: []health.CheckDefinition{cpu.NUMANodeCount()}, want: [][]string{{
			cpusignals.NUMANodeOnlineCount.ID(),
		}}, bridged: 1},
		{name: "no domain check runs", runnable: []health.CheckDefinition{gpu.GPUPowerState()}},
		{name: "check needs an unmapped signal", runnable: []health.CheckDefinition{
			cpu.NUMANodeCount(), {ID: "custom", Category: health.CPU, RequiredSignals: []string{"unmapped"}},
		}, want: [][]string{{cpusignals.NUMANodeOnlineCount.ID()}}, bridged: 1},
		{name: "check needs only an unmapped signal", runnable: []health.CheckDefinition{
			{ID: "custom", Category: health.CPU, RequiredSignals: []string{"unmapped"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &recordingSensor{id: "test.cpu", signals: cpusignals.All()}
			var runnable []*health.CheckDefinition
			for i := range test.runnable {
				runnable = append(runnable, &test.runnable[i])
			}
			sensors, err := New().collectSensors(time.Second, []collect.Sensor{source}, []CollectAdapter{collectadapters.CPU()}, requiredFor(runnable))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(source.selected, test.want) || len(sensors) != test.bridged {
				t.Fatalf("selected=%v bridged=%d; want %v and %d", source.selected, len(sensors), test.want, test.bridged)
			}
		})
	}
}

// An adapter left with nothing to collect is not built, so a built-in check
// its mapping cannot feed would silently skip. Every built-in check of a
// collect-backed domain must therefore be fed by that domain's adapter, from
// signals its built-in source declares.
func TestBuiltinCollectFeedsEveryBuiltinCheck(t *testing.T) {
	sources, adapters := New().builtinCollect(nil)
	adaptersByCategory := make(map[health.Category]int)
	for _, adapter := range adapters {
		adaptersByCategory[adapter.Category()]++
	}
	for _, cd := range catalog.New().List(catalog.Filter{}) {
		if got := adaptersByCategory[cd.Category]; got != 1 {
			t.Errorf("built-in check %s: category %s has %d built-in collect adapters, want 1", cd.ID, cd.Category, got)
		}
	}
	var produced []string
	for _, source := range sources {
		for _, signal := range source.Signals() {
			produced = append(produced, signal.ID())
		}
	}
	for _, adapter := range adapters {
		fed := make(map[string]bool)
		for collectID, catalogSignals := range adapter.Signals() {
			if !slices.Contains(produced, collectID) {
				t.Errorf("adapter %q reads %q, which no built-in source declares", adapter.Name(), collectID)
			}
			for _, s := range catalogSignals {
				fed[s] = true
			}
		}
		for _, cd := range catalog.New().List(catalog.Filter{}) {
			if cd.Category != adapter.Category() {
				continue
			}
			for _, s := range cd.RequiredSignals {
				// gpu_id comes from each observation's GPU target.
				if !fed[s] && s != gpu.SignalGPUID {
					t.Errorf("built-in check %s requires %q, which adapter %q does not feed", cd.ID, s, adapter.Name())
				}
			}
		}
	}
}

// With no signals required, no collect source runs, so every sensor returned
// is a legacy one, and the stock CLI has none left.
func TestDefaultRunHasNoLegacySensors(t *testing.T) {
	a := New()
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	cycle, err := newCycle(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sensors, err := a.sensors(cycle, requiredSignals{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sensors {
		t.Errorf("default run collects %q with a legacy sensor", s.Name())
	}
}

// Off GB200/GB300, numa_node_count is an architecture skip, so NUMA is not
// collected and the CPU measurement carries a failure for it. cpu_count must
// still pass: a measurement error fails only checks requiring that signal.
func TestCollectSensorsUncollectedSiblingSignalLeavesCheckPassing(t *testing.T) {
	cat, _, err := buildCatalog(nil, nil, "H100", health.DriverVersion{}, health.OperatorInfra)
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sysfs := fstest.MapFS{
		"sys/devices/system/cpu/possible": {Data: []byte("0-143\n")},
		"sys/devices/system/cpu/online":   {Data: []byte("0-143\n")},
	}
	sensors, err := New().collectSensors(time.Second, []collect.Sensor{cpusensor.New(sysfs)}, []CollectAdapter{collectadapters.CPU()}, requiredFor(runnable))
	if err != nil {
		t.Fatal(err)
	}
	var measurements []health.Measurement
	for _, sensor := range sensors {
		m, measureErr := sensor.Measure(context.Background())
		if measureErr != nil {
			t.Fatal(measureErr)
		}
		measurements = append(measurements, m...)
	}
	verdict, err := cat.EvaluateAllWithOptions(catalog.EvaluationOptions{}, measurements...)
	if err != nil {
		t.Fatal(err)
	}
	var passed, skipped bool
	for _, c := range verdict.CheckResults {
		passed = passed || (c.CheckID == "cpu_count" && c.Severity == health.Pass)
	}
	for _, c := range verdict.SkippedChecks {
		skipped = skipped || (c.CheckID == "numa_node_count" && c.SkipReason == health.ArchitectureUnsupported)
	}
	if !passed || !skipped {
		t.Fatalf("want cpu_count PASS and numa_node_count ARCHITECTURE_UNSUPPORTED: results=%+v skipped=%+v", verdict.CheckResults, verdict.SkippedChecks)
	}
}

func TestCollectAdapterOutsideCategoryFails(t *testing.T) {
	var got error
	adapter := collectAppAdapter{
		name: "strays", signals: map[string][]string{"test.a": {"a"}},
		build: func([]*observation.Observation) []health.Measurement {
			return []health.Measurement{gpu.GPUMeasurement{}}
		},
		errored: func(err error) []health.Measurement { got = err; return nil },
	}
	source := &recordingSensor{id: "test.source", signals: claimSignals()}
	sensors, err := New().WithCollect(source, "test.a").WithCollectAdapters(adapter).collectSensors(time.Second, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if measurements, _ := sensors[0].Measure(context.Background()); measurements != nil || got == nil || !strings.Contains(got.Error(), "declares category") {
		t.Fatalf("measurements=%v err=%v; want the stray category rejected", measurements, got)
	}
}

func TestCollectSensorsSkipsCallerSensorsNoRunnableCheckNeeds(t *testing.T) {
	caller := &recordingSensor{id: "test.caller", signals: claimSignals()}
	var produced []string
	builtin := &recordingSensor{id: "test.builtin", signals: claimSignals()}
	a := New().WithCollect(caller, "test.a")
	if _, err := a.collectSensors(time.Second, []collect.Sensor{builtin}, []CollectAdapter{sourceAdapter(&produced)}, requiredSignals{health.Node: {"b": true}}); err != nil {
		t.Fatalf("an unneeded caller sensor became a registration error: %v", err)
	}
	if len(caller.selected) != 0 {
		t.Fatalf("caller sensor was measured for unneeded signals: %v", caller.selected)
	}
}

func TestCollectSensorsValidatesRegistrationsRegardlessOfNeeds(t *testing.T) {
	for _, test := range []struct {
		name    string
		want    string
		sensors []*recordingSensor
	}{
		{name: "overlapping claims", want: "claim each signal from one sensor", sensors: []*recordingSensor{
			{id: "test.caller", signals: claimSignals()}, {id: "test.other", signals: claimSignals()},
		}},
		{name: "sensor no adapter reads", want: "which no adapter requests", sensors: []*recordingSensor{
			{id: "test.unrelated", signals: collect.Signals{collect.NewSignal("test.other", "")}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var produced []string
			a := New()
			for _, s := range test.sensors {
				a.WithCollect(s, claimAll(s)...)
			}
			_, err := a.collectSensors(time.Second, nil, []CollectAdapter{sourceAdapter(&produced)}, requiredSignals{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want %q even though no check needs any signal", err, test.want)
			}
			for _, s := range test.sensors {
				if len(s.selected) != 0 {
					t.Fatalf("invalid registration %q was measured", s.id)
				}
			}
		})
	}
}

// modelHost is fakeHost with GPUs of one model; no GPUs fails discovery.
type modelHost struct {
	fakeHost
	model string
	gpus  int
}

func (h modelHost) gpuInventory() *gpuinventory.Inventory {
	return gpuinventory.New(func() (gpuinventory.Reader, error) { return inventoryReader{gpus: h.gpus, model: h.model}, nil })
}

// gpu.power.draw is instantaneous on GA100 and older, so only architectures
// that average it may export it as average_power_draw_w.
func TestPowerDrawIsAverageOnlyOnAveragingArchitectures(t *testing.T) {
	for _, tt := range []struct {
		name, model string
		want        string // "" for the 700 W value, else the error text
		gpus        int
	}{
		{name: "H100", model: "NVIDIA H100 80GB HBM3", gpus: 4},
		{name: "H200", model: "NVIDIA H200", gpus: 4},
		{name: "GB200", model: "NVIDIA GB200", gpus: 4},
		{name: "B300", model: "NVIDIA B300 SXM6 AC", gpus: 4},
		{name: "A100", model: "NVIDIA A100-SXM4-80GB", gpus: 4, want: `NOT_SUPPORTED): power draw is not a one-second average on architecture "A100"`},
		{name: "unknown", model: "Tesla V100-SXM2-32GB", gpus: 4, want: `on architecture "unknown"`},
		{name: "discovery failure", gpus: 0, want: `"GPUArchitecture" failed with COLLECTION_ERROR_CATEGORY_UNAVAILABLE: GPU architecture discovery: no GPUs enumerated`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := cadenceRunApp()
			a.host = modelHost{fakeHost: fakeHost{b: spyBackend{called: new(bool)}}, model: tt.model, gpus: tt.gpus}
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"collect", "--node-id", testNodeID}, &stdout, &stderr); code != exitOK {
				t.Fatalf("collect exit = %d; stderr = %q", code, stderr.String())
			}
			var batch observation.ObservationBatch
			if err := protojson.Unmarshal(stdout.Bytes(), &batch); err != nil {
				t.Fatal(err)
			}
			var exported int
			for _, o := range batch.GetObservations() {
				if o.GetSignalId() != "gpu."+gpu.SignalAveragePowerDrawW {
					continue
				}
				exported++
				if tt.want == "" {
					if o.GetValue().GetDoubleValue() != 700 {
						t.Errorf("average power draw = %v, want 700 W", o)
					}
				} else if !strings.Contains(o.GetCollectionError().GetDetail(), tt.want) {
					t.Errorf("average power draw = %v, want an error containing %q", o, tt.want)
				}
			}
			if exported != 4 {
				t.Fatalf("exported %d average power draws, want 4:\n%s", exported, stdout.String())
			}
		})
	}
}

func TestAveragePowerDrawChecksStayOnAveragingArchitectures(t *testing.T) {
	category := health.GPU
	var found bool
	for _, check := range catalog.New().List(catalog.Filter{Category: &category}) {
		if !slices.Contains(check.RequiredSignals, gpu.SignalAveragePowerDrawW) {
			continue
		}
		found = true
		if len(check.Architectures) == 0 {
			t.Errorf("%s reads %s on every architecture", check.ID, gpu.SignalAveragePowerDrawW)
		}
		for _, arch := range check.Architectures {
			if !slices.Contains(averagedPowerDrawArchitectures, arch) {
				t.Errorf("%s reads %s on %s, which does not average it", check.ID, gpu.SignalAveragePowerDrawW, arch)
			}
		}
	}
	if !found {
		t.Fatal("no built-in check reads average_power_draw_w")
	}
}

// A source that reports nothing fails the adapter's checks rather than
// leaving them skipped and the run PASS.
func TestGPUAdapterFailsWhenNothingCollected(t *testing.T) {
	adapter, err := GPUAdapter[eccMeasurement]("ecc", map[string]string{
		gpusignals.ECCDoubleBitVolatileTotal.ID(): "ecc_double_bit",
		gpusignals.PerformanceState.ID():          "pstate",
	})
	if err != nil {
		t.Fatal(err)
	}
	ms := adapter.Build(nil)
	if len(ms) != 1 || health.KeyOf(ms[0]) != health.SourceEntity("collect.ecc") || len(ms[0].Errors()) == 0 {
		t.Fatalf("Build(nil) = %+v, want one errored source-level measurement", ms)
	}
}

// lostSlotReader reports three GB200s and a fourth slot whose device handle
// could not be read.
type lostSlotReader struct{}

func (lostSlotReader) ID() string { return "fake" }

func (lostSlotReader) Read(ctx context.Context) (*observation.GPUInventorySnapshot, error) {
	snapshot, err := inventoryReader{gpus: 3, model: "NVIDIA GB200"}.Read(ctx)
	snapshot.GetInventory().Entries = append(snapshot.GetInventory().Entries, &observation.GPUInventoryEntry{
		SourceIndex: 3, Gpu: &observation.GPUDevice{}, CollectionError: &observation.CollectionError{
			Source: "nvml", Operation: "DeviceGetHandleByIndex", Category: observation.CollectionErrorCategoryUnavailable, SourceError: "ERROR_GPU_IS_LOST",
		},
	})
	return snapshot, err
}

// A GPU no source can identify fails gpu_count, and the GPU checks that could
// not read it name gpu_count as the cause.
func TestRunNamesGPUCountForLostGPU(t *testing.T) {
	f := healthyGB200()
	a := fixtureApp(f)
	now := timestamppb.Now()
	var dcgm, processes []*observation.Observation
	for _, g := range f.gpus[:3] {
		dcgm = append(dcgm, g.dcgm(now)...)
		processes = append(processes, g.processes(now)...)
	}
	for _, o := range f.gpus[3].dcgm(now) {
		o.Target = observation.GPUSourceLocalTarget("dcgm-index-3")
		dcgm = append(dcgm, o)
	}
	host := a.host.(sharedInventoryHost)
	host.inventory = gpuinventory.New(func() (gpuinventory.Reader, error) { return lostSlotReader{}, nil })
	host.gpu = []collect.Sensor{
		fixedSensor{id: "gpu.dcgm", signals: dcgmGPUSignals, obs: dcgm},
		fixedSensor{id: "gpu.nvml", signals: processGPUSignals, obs: processes},
	}
	a.host = host

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitUnhealthy, stderr.String())
	}
	var v health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	countFailed, explained := false, false
	for _, r := range v.CheckResults {
		countFailed = countFailed || (r.CheckID == "gpu_count" && r.Severity == health.Critical)
		explained = explained || strings.Contains(strings.Join(r.Messages, " "), `explained by dependency "gpu_count"`)
	}
	if !countFailed || !explained {
		t.Errorf("gpu_count critical = %v, a GPU check names gpu_count = %v; verdict = %+v", countFailed, explained, v.CheckResults)
	}
}

// TestPCIGPUAdapterJoinsOnlyInventoryGPUs filters unmatched devices while retaining source failures.
func TestPCIGPUAdapterJoinsOnlyInventoryGPUs(t *testing.T) {
	var got []*observation.Observation
	adapter := pciGPUAdapter{
		CollectAdapter: collectAppAdapter{build: func(obs []*observation.Observation) []health.Measurement { got = obs; return nil }},
		lookup: func() *observation.GPUInventorySnapshot {
			return pciInventory(map[string]string{"0000:01:00": "GPU-0"})
		},
	}
	width := &observation.Value{Kind: &observation.Value_UintValue{UintValue: 16}}
	failure := &observation.CollectionError{Category: observation.CollectionErrorCategoryPermissionDenied, Detail: "PCI root unreadable"}
	gpuProperty := &observation.Observation{SignalId: "pci.link.width.max", Target: observation.PCITarget("0000:01:00.0"), Outcome: &observation.Observation_Value{Value: width}}
	nonGPU := &observation.Observation{SignalId: "pci.link.width.max", Target: observation.PCITarget("0000:02:00.0"), Outcome: &observation.Observation_Value{Value: width}}
	sourceFailure := &observation.Observation{SignalId: "pci.link.width.max", Target: observation.NodeTarget(""), Outcome: &observation.Observation_CollectionError{CollectionError: failure}}
	adapter.Build(adapter.prepare([]*observation.Observation{gpuProperty, nonGPU, sourceFailure}))
	if len(got) != 2 {
		t.Fatalf("got %d observations, want GPU and source failure", len(got))
	}
	if got[0].GetTarget().GetGpu().GetUuid() != "GPU-0" {
		t.Fatalf("GPU target = %v", got[0].GetTarget())
	}
	if got[1].GetTarget() != nil {
		t.Fatalf("source failure target = %v", got[1].GetTarget())
	}
	if got[0].GetValue() != width || got[1].GetCollectionError() != failure {
		t.Fatalf("retargeting changed outcomes: %v", got)
	}
	if gpuProperty.GetTarget().GetPci() == nil {
		t.Fatal("input observation was mutated")
	}
}

// TestPCIGPUAdapterKeepsClassificationFailureOnAffectedGPU prevents one bad device from hiding healthy GPUs.
func TestPCIGPUAdapterKeepsClassificationFailureOnAffectedGPU(t *testing.T) {
	files := fstest.MapFS{
		"sys/bus/pci/devices/0000:01:00.0/class":          {Data: []byte("0x030200")},
		"sys/bus/pci/devices/0000:01:00.0/vendor":         {Data: []byte("0x10de")},
		"sys/bus/pci/devices/0000:01:00.0/max_link_width": {Data: []byte("16")},
		"sys/bus/pci/devices/0000:01:00.0/max_link_speed": {Data: []byte("64.0 GT/s PCIe")},
		"sys/bus/pci/devices/0000:02:00.0/class":          {Data: []byte("malformed")},
	}
	sensor := pcisensor.NewGPUs(files)
	batch, err := sensor.Measure(context.Background(), collect.WithSignalID("pci.link.width.max", "pci.link.generation.max"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
		return pciInventory(map[string]string{"0000:01:00": "GPU-0", "0000:02:00": "GPU-1"})
	}}
	measurements := adapter.Build(adapter.prepare(batch.Observations))
	if len(measurements) != 2 {
		t.Fatalf("got %d measurements, want two GPUs", len(measurements))
	}
	if got := pciVerdicts(t, measurements); !reflect.DeepEqual(got, map[string]health.Severity{"GPU-0": health.Pass, "GPU-1": health.Unknown}) {
		t.Fatalf("PCIe verdicts = %v", got)
	}
}

// TestPCICollectionLoadsInventoryWithoutPowerDraw exercises PCIe-only collection
// when no successful power draw can populate the shared inventory cache.
func TestPCICollectionLoadsInventoryWithoutPowerDraw(t *testing.T) {
	a := fixtureApp(healthyGB200())
	host := a.host.(sharedInventoryHost)
	host.gpu = nil
	a.host = host
	cycle, err := newCycle(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if host.inventory.Cached(cycle) != nil {
		t.Fatal("inventory was already populated")
	}
	sources, adapters := a.builtinCollect(cycle)
	sensors, err := a.collectSensorsIn(cycle, sources, adapters, requiredSignals{health.PCIe: {pcie.SignalPCIeMaxLinkWidth: true, pcie.SignalPCIeMaxLinkGeneration: true}})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, group := range toolkit.CollectMeasurementsWithTimeout(time.Second, sensors...) {
		for _, measurement := range group.Measurements {
			m, ok := measurement.(pcie.PCIeMeasurement)
			if !ok {
				continue
			}
			if m.GPUID == "" || m.PCIeMaxLinkWidth != 16 || m.PCIeMaxLinkGeneration != 6 {
				t.Fatalf("PCIe measurement = %+v", m)
			}
			count++
		}
	}
	if count != 4 {
		t.Fatalf("GPU measurements = %d, want 4", count)
	}
	if host.inventory.Cached(cycle) == nil {
		t.Fatal("PCI lookup did not populate inventory")
	}
}

// pciVerdicts requires one evaluated result per GPU UUID.
func pciVerdicts(t *testing.T, measurements []health.Measurement) map[string]health.Severity {
	t.Helper()
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	results := make(map[string]health.Severity)
	for _, measurement := range measurements {
		m := measurement.(pcie.PCIeMeasurement)
		if _, duplicate := results[m.GPUID]; duplicate {
			t.Fatalf("duplicate PCIe measurement for %s", m.GPUID)
		}
		v, err := cat.Evaluate("gpu_pcie_max_link", m)
		if err != nil || v.Skipped {
			t.Fatalf("PCIe verdict=%+v err=%v", v, err)
		}
		results[m.GPUID] = v.Severity
	}
	return results
}

// TestPCIGPUAdapterSourceOutcomesAvoidInventory retains root failures and
// empty-enumeration outcomes without opening the GPU inventory.
func TestPCIGPUAdapterSourceOutcomesAvoidInventory(t *testing.T) {
	for _, outcome := range []*observation.Observation{
		observation.NewCollectionErrorObservation("pci.link.width.max", nil, "pci.sysfs", "root", observation.NodeTarget(""), timestamppb.Now(), &observation.CollectionError{Category: observation.CollectionErrorCategoryPermissionDenied}),
		observation.NewNoValueObservation("pci.link.width.max", nil, "pci.sysfs", observation.NodeTarget(""), timestamppb.Now(), &observation.NoValue{Reason: observation.NoValueReasonNoTargets}),
	} {
		var got []*observation.Observation
		adapter := pciGPUAdapter{CollectAdapter: collectAppAdapter{build: func(obs []*observation.Observation) []health.Measurement { got = obs; return nil }}, lookup: func() *observation.GPUInventorySnapshot { t.Fatal("source-only outcome opened inventory"); return nil }}
		adapter.Build(adapter.prepare([]*observation.Observation{outcome}))
		if len(got) != 1 || got[0].Target != nil || got[0].Outcome != outcome.Outcome || outcome.GetTarget().GetNode() == nil {
			t.Fatalf("source outcome changed: %v", got)
		}
	}
}

// TestPCIGPUAdapterMissingGPU reports incomplete enumeration per inventory GPU.
func TestPCIGPUAdapterMissingGPU(t *testing.T) {
	for _, address := range []string{"0000:01:00.0", "0000:03:00.0"} {
		calls := 0
		adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
			calls++
			return pciInventory(map[string]string{"0000:01:00": "GPU-0", "0000:02:00": "GPU-1"})
		}}
		var obs []*observation.Observation
		for signal, value := range map[string]uint64{"pci.link.width.max": 16, "pci.link.generation.max": 5} {
			unit := "1"
			obs = append(obs, &observation.Observation{SignalId: signal, Source: "pci.sysfs", Unit: &unit, ObservedAt: timestamppb.Now(), Target: observation.PCITarget(address), Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}}})
		}
		want := map[string]health.Severity{"GPU-0": health.Pass, "GPU-1": health.Unknown}
		if address == "0000:03:00.0" {
			want["GPU-0"] = health.Unknown
		}
		if got := pciVerdicts(t, adapter.Build(adapter.prepare(obs))); !reflect.DeepEqual(got, want) || calls != 1 {
			t.Fatalf("address %s: verdicts=%v want=%v lookups=%d", address, got, want, calls)
		}
	}
}

// TestPCIePartialClaims permits separate caller and stock sources with the
// same public ID to provide width and generation independently.
func TestPCIePartialClaims(t *testing.T) {
	files := func(width, speed string) fstest.MapFS {
		return fstest.MapFS{
			"sys/bus/pci/devices/0000:01:00.0/class":          {Data: []byte("0x030200")},
			"sys/bus/pci/devices/0000:01:00.0/vendor":         {Data: []byte("0x10de")},
			"sys/bus/pci/devices/0000:01:00.0/max_link_width": {Data: []byte(width)},
			"sys/bus/pci/devices/0000:01:00.0/max_link_speed": {Data: []byte(speed)},
		}
	}
	for _, claim := range []string{"pci.link.width.max", "pci.link.generation.max"} {
		caller := pcisensor.NewGPUs(files("16", "32.0 GT/s PCIe"))
		stock := pcisensor.NewGPUs(files("8", "16.0 GT/s PCIe"))
		adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
			return pciInventory(map[string]string{"0000:01:00": "GPU-0"})
		}}
		sensors, err := New().WithCollect(caller, claim).collectSensors(time.Second, []collect.Sensor{stock}, []CollectAdapter{adapter}, requiredSignals{health.PCIe: nil})
		if err != nil {
			t.Fatal(err)
		}
		if len(sensors) != 1 {
			t.Fatalf("sensors=%d", len(sensors))
		}
		ms, err := sensors[0].Measure(context.Background())
		if err != nil || len(ms) != 1 {
			t.Fatalf("measurements=%v err=%v", ms, err)
		}
		m := ms[0].(pcie.PCIeMeasurement)
		width, generation := int64(16), int64(4)
		if claim == "pci.link.generation.max" {
			width, generation = 8, 5
		}
		if m.GPUID != "GPU-0" || m.PCIeMaxLinkWidth != width || m.PCIeMaxLinkGeneration != generation || len(m.Errors()) != 0 {
			t.Fatalf("claim %s: %+v errors=%v", claim, m, m.Errors())
		}
		if caller.ID() != pcisensor.ID || stock.ID() != pcisensor.ID {
			t.Fatal("registration changed public sensor IDs")
		}
	}
}

type stalledPCIFiles struct {
	fstest.MapFS
	release <-chan struct{}
}

func (f stalledPCIFiles) ReadDir(name string) ([]fs.DirEntry, error) {
	<-f.release
	return f.MapFS.ReadDir(name)
}

func TestPCIePartialClaimTimeoutKeepsStockReading(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	files := fstest.MapFS{
		"sys/bus/pci/devices/0000:01:00.0/class":          {Data: []byte("0x030200")},
		"sys/bus/pci/devices/0000:01:00.0/vendor":         {Data: []byte("0x10de")},
		"sys/bus/pci/devices/0000:01:00.0/max_link_speed": {Data: []byte("32.0 GT/s PCIe")},
	}
	caller := pcisensor.NewGPUs(stalledPCIFiles{MapFS: files, release: release})
	stock := pcisensor.NewGPUs(files)
	adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
		return pciInventory(map[string]string{"0000:01:00.0": "GPU-0"})
	}}
	sensors, err := New().WithCollect(caller, pcisignals.MaxLinkWidth.ID()).collectSensors(100*time.Millisecond, []collect.Sensor{stock}, []CollectAdapter{adapter}, requiredSignals{health.PCIe: nil})
	if err != nil || len(sensors) != 1 {
		t.Fatalf("sensors=%v err=%v", sensors, err)
	}
	ms, err := sensors[0].Measure(context.Background())
	if err != nil || len(ms) != 1 {
		t.Fatalf("measurements=%v err=%v", ms, err)
	}
	m := ms[0].(pcie.PCIeMeasurement)
	if m.GPUID != "GPU-0" || m.PCIeMaxLinkGeneration != 5 {
		t.Fatalf("stock generation lost: %+v", m)
	}
	if failures := m.Errors(); len(failures) != 1 || !slices.Equal(failures[0].FailedSignals(), []string{pcie.SignalPCIeMaxLinkWidth}) || !errors.Is(failures[0].Err(), health.ErrCollectionTimeout) {
		t.Fatalf("failures=%v, want caller width timeout", failures)
	}
}

// TestPCIeUnsupportedScope accepts device capabilities but rejects a node-wide
// NOT_SUPPORTED claim as a producer error rather than broadcasting it to GPUs.
func TestPCIeUnsupportedScope(t *testing.T) {
	signals := collect.Signals{collect.NewSignal("pci.link.width.max", "1"), collect.NewSignal("pci.link.generation.max", "1")}
	for _, node := range []bool{false, true} {
		target := observation.PCITarget("0000:01:00.0")
		if node {
			target = observation.NodeTarget("")
		}
		obs := []*observation.Observation{
			observation.NewNoValueObservation(signals[0].ID(), signals[0].Unit(), "pci.sysfs", target, timestamppb.Now(), &observation.NoValue{Reason: observation.NoValueReasonNotSupported}),
			{SignalId: signals[1].ID(), Source: "pci.sysfs", Unit: signals[1].Unit(), ObservedAt: timestamppb.Now(), Target: observation.PCITarget("0000:01:00.0"), Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: 5}}}},
		}
		source := fixedSensor{id: "pci.sysfs", signals: signals, obs: obs}
		adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
			return pciInventory(map[string]string{"0000:01:00": "GPU-0"})
		}}
		sensors, err := New().collectSensors(time.Second, []collect.Sensor{source}, []CollectAdapter{adapter}, requiredSignals{health.PCIe: nil})
		if err != nil || len(sensors) != 1 {
			t.Fatalf("sensors=%v err=%v", sensors, err)
		}
		ms, err := sensors[0].Measure(context.Background())
		if err != nil || len(ms) != 1 {
			t.Fatalf("measurements=%v err=%v", ms, err)
		}
		v, err := catalog.New(catalog.WithArchitecture("GB200")).Evaluate("gpu_pcie_max_link", ms[0])
		want := health.Info
		if node {
			want = health.Unknown
		}
		if err != nil || v.Skipped || v.Severity != want {
			t.Fatalf("node=%v verdict=%+v err=%v", node, v, err)
		}
	}
}

// virtualizationHost supplies classification independently of basic discovery.
type virtualizationHost struct {
	hostBackend
	classify func(string) (bool, error)
}

func (h virtualizationHost) gpuVirtualization(ctx context.Context, uuid string) (bool, error) {
	return h.classify(uuid)
}

// TestRunGuestPCIe evaluates exposed capabilities and handles identified VFs.
func TestRunGuestPCIe(t *testing.T) {
	for _, tc := range []struct {
		failure error
		name    string
		width   string
		want    health.Severity
		virtual bool
		vgpu    bool
	}{
		{name: "physical", width: "16", want: health.Pass},
		{name: "unidentified narrow device", width: "1", want: health.Critical},
		{name: "identified VF", width: "1", virtual: true, want: health.Info},
		{name: "vGPU without physfn", width: "1", vgpu: true, want: health.Info},
		{name: "classification failure", width: "16", failure: fmt.Errorf("classification unavailable"), want: health.Unknown},
		{name: "classification timeout", width: "16", failure: collect.ErrCollectionTimeout, want: health.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := fixtureApp(healthyGB200())
			calls := make(map[string]int)
			a.host = virtualizationHost{hostBackend: a.host, classify: func(uuid string) (bool, error) {
				calls[uuid]++
				return tc.vgpu, tc.failure
			}}
			files := fstest.MapFS{
				"sys/bus/pci/devices/0000:01:00.0/class":          {Data: []byte("0x030200")},
				"sys/bus/pci/devices/0000:01:00.0/vendor":         {Data: []byte("0x10de")},
				"sys/bus/pci/devices/0000:01:00.0/max_link_width": {Data: []byte(tc.width)},
				"sys/bus/pci/devices/0000:01:00.0/max_link_speed": {Data: []byte("32.0 GT/s PCIe")},
			}
			if tc.virtual {
				files["sys/bus/pci/devices/0000:01:00.0/physfn"] = &fstest.MapFile{}
			}
			for slot := 2; slot <= 4; slot++ {
				for _, field := range []string{"class", "vendor", "max_link_width", "max_link_speed", "physfn"} {
					if entry := files["sys/bus/pci/devices/0000:01:00.0/"+field]; entry != nil {
						files[fmt.Sprintf("sys/bus/pci/devices/0000:%02d:00.0/%s", slot, field)] = entry
					}
				}
			}
			for i := range a.collectSources {
				if a.collectSources[i].sensor.ID() == pcisensor.ID {
					a.collectSources[i].sensor = pcisensor.NewGPUs(files)
				}
			}
			var stdout, stderr bytes.Buffer
			wantExit := exitOK
			if tc.want == health.Critical || tc.want == health.Unknown {
				wantExit = exitUnhealthy
			}
			if code := a.Run([]string{"run"}, &stdout, &stderr); code != wantExit {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			var verdict health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(verdict.SkippedChecks, func(v health.CheckVerdict) bool { return v.CheckID == "gpu_pcie_max_link" }) {
				t.Fatalf("unexpected PCIe skip: %+v", verdict)
			}
			count := 0
			for _, v := range verdict.CheckResults {
				if v.CheckID == "gpu_pcie_max_link" {
					count++
					if v.Severity != tc.want {
						t.Fatalf("PCIe verdict=%+v, want %v", v, tc.want)
					}
					if tc.want == health.Info && !v.RecommendedAction.Equal(health.NoAction) {
						t.Fatalf("VF action=%+v", v.RecommendedAction)
					}
				}
			}
			if count != 4 {
				t.Fatalf("PCIe results=%d, want 4", count)
			}
			wantCalls := 1
			if tc.virtual {
				wantCalls = 0
			}
			for slot := range 4 {
				uuid := fmt.Sprintf("GPU-%d", slot)
				if calls[uuid] != wantCalls {
					t.Fatalf("classification calls[%s]=%d, want %d", uuid, calls[uuid], wantCalls)
				}
			}
		})
	}
}

// TestPCIeClassificationPreservesOutcomes keeps source errors and unsupported
// values intact, without reading virtualization metadata for either outcome.
func TestPCIeClassificationPreservesOutcomes(t *testing.T) {
	target := observation.GPUTarget("GPU-0")
	signals := collect.Signals{pcisignals.MaxLinkWidth, pcisignals.MaxLinkGeneration}
	obs := []*observation.Observation{
		observation.NewNoValueObservation(signals[0].ID(), signals[0].Unit(), "pci.sysfs", target, timestamppb.Now(), &observation.NoValue{Reason: observation.NoValueReasonNotSupported}),
		observation.NewCollectionErrorObservation(signals[1].ID(), signals[1].Unit(), "pci.sysfs", "read", target, timestamppb.Now(), &observation.CollectionError{Category: observation.CollectionErrorCategoryPermissionDenied}),
	}
	got := classifyPCIe(slices.Clone(obs), func([]string) (map[string]pciVirtualization, error) {
		t.Fatal("unexpected classification")
		return nil, nil
	})
	for i := range obs {
		if got[i] != obs[i] {
			t.Fatalf("outcome %d changed: %v", i, got[i])
		}
	}
}

// TestPCIeVirtualizationTimeout preserves completed classifications and avoids
// further calls to a wedged NVML source.
func TestPCIeVirtualizationTimeout(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan string, 3)
	defer close(release)
	a := New()
	a.host = virtualizationHost{hostBackend: fixtureApp(healthyGB200()).host, classify: func(uuid string) (bool, error) {
		entered <- uuid
		if uuid == "GPU-1" {
			<-release
		}
		return false, nil
	}}
	const timeout = 100 * time.Millisecond
	cycle, err := newCycle(timeout)
	if err != nil {
		t.Fatal(err)
	}
	_, adapters := a.builtinCollect(cycle)
	for _, adapter := range adapters {
		pci, ok := adapter.(pciGPUAdapter)
		if !ok {
			continue
		}
		signals := collect.Signals{pcisignals.MaxLinkWidth, pcisignals.MaxLinkGeneration}
		var obs []*observation.Observation
		for slot := range 3 {
			obs = append(obs, pciCapabilityObservations(fmt.Sprintf("0000:%02x:00.0", slot+1))...)
		}
		source := fixedSensor{id: pcisensor.ID, signals: signals, obs: obs}
		sensors, err := a.collectSensorsIn(cycle, []collect.Sensor{source}, []CollectAdapter{pci}, requiredSignals{health.PCIe: nil})
		if err != nil || len(sensors) != 1 {
			t.Fatalf("sensors=%v err=%v", sensors, err)
		}
		groups := toolkit.CollectMeasurementsWithTimeout(timeout, sensors...)
		want := map[string]health.Severity{"GPU-0": health.Pass, "GPU-1": health.Unknown, "GPU-2": health.Unknown, "GPU-3": health.Unknown}
		if got := pciVerdicts(t, groups[0].Measurements); !reflect.DeepEqual(got, want) {
			t.Fatalf("verdicts=%v, want %v", got, want)
		}
		for _, m := range groups[0].Measurements {
			if id := m.(pcie.PCIeMeasurement).GPUID; id == "GPU-1" || id == "GPU-2" {
				if got := m.Errors(); len(got) != 1 || !errors.Is(got[0].Err(), health.ErrCollectionTimeout) {
					t.Fatalf("%s errors=%v, want timeout", id, got)
				}
			}
		}
		if len(entered) != 2 || <-entered != "GPU-0" || <-entered != "GPU-1" {
			t.Fatal("expected one NVML call each for GPU-0 and GPU-1")
		}
		return
	}
	t.Fatal("missing PCIe adapter")
}

// pciInventory supplies GPU identities for PCI adapter fixtures.
func pciInventory(ids map[string]string) *observation.GPUInventorySnapshot {
	inventory := &observation.GPUInventory{}
	for address, uuid := range ids {
		inventory.Entries = append(inventory.Entries, &observation.GPUInventoryEntry{Gpu: &observation.GPUDevice{Uuid: &uuid, BusId: &address}})
	}
	return &observation.GPUInventorySnapshot{Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: inventory}}
}

func pciCapabilityObservations(address string) []*observation.Observation {
	var obs []*observation.Observation
	for _, capability := range []struct {
		signal collect.Signal
		value  uint64
	}{{pcisignals.MaxLinkWidth, 16}, {pcisignals.MaxLinkGeneration, 5}} {
		obs = append(obs, &observation.Observation{
			SignalId: capability.signal.ID(), Unit: capability.signal.Unit(), Source: pcisensor.ID,
			Target: observation.PCITarget(address), ObservedAt: timestamppb.Now(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: capability.value}}},
		})
	}
	return obs
}

func TestPCIGPUAdapterMatchesFullPCIAddress(t *testing.T) {
	files := fstest.MapFS{
		"sys/bus/pci/devices/0000:41:00.0/class":          {Data: []byte("0x030200")},
		"sys/bus/pci/devices/0000:41:00.0/vendor":         {Data: []byte("0x10de")},
		"sys/bus/pci/devices/0000:41:00.0/max_link_width": {Data: []byte("16")},
		"sys/bus/pci/devices/0000:41:00.0/max_link_speed": {Data: []byte("32.0 GT/s PCIe")},
		"sys/bus/pci/devices/0000:41:00.4/class":          {Data: []byte("0x030200")},
		"sys/bus/pci/devices/0000:41:00.4/vendor":         {Data: []byte("0x10de")},
		"sys/bus/pci/devices/0000:41:00.4/physfn":         {},
	}
	sensor := pcisensor.NewGPUs(files)
	batch, err := sensor.Measure(context.Background(), collect.WithSignalID(pcisignals.MaxLinkWidth.ID(), pcisignals.MaxLinkGeneration.ID()))
	if err != nil {
		t.Fatal(err)
	}
	adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot {
		return pciInventory(map[string]string{"00000000:41:00.0": "GPU-PF", "00000000:41:00.4": "GPU-VF"})
	}}
	want := map[string]health.Severity{"GPU-PF": health.Pass, "GPU-VF": health.Info}
	if got := pciVerdicts(t, adapter.Build(adapter.prepare(batch.Observations))); !reflect.DeepEqual(got, want) {
		t.Fatalf("verdicts=%v, want %v", got, want)
	}
}

func TestPCIGPUAdapterKeepsUnidentifiedInventorySlot(t *testing.T) {
	snapshot := pciInventory(map[string]string{"0000:01:00.0": "GPU-0"})
	snapshot.Source = "nvml"
	snapshot.GetInventory().Entries = append(snapshot.GetInventory().Entries, &observation.GPUInventoryEntry{
		SourceIndex: 1,
		FieldErrors: map[string]*observation.CollectionError{"uuid": {
			Source: "nvml", Operation: "GetUUID", Category: observation.CollectionErrorCategoryUnavailable, Detail: "UUID read failed",
		}},
	})
	adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot { return snapshot }}
	obs := append(pciCapabilityObservations("0000:01:00.0"), pciCapabilityObservations("0000:02:00.0")...)
	ms := adapter.Build(adapter.prepare(obs))
	want := map[string]health.Severity{"GPU-0": health.Pass, `slot:"nvml"/"1"`: health.Unknown}
	if got := pciVerdicts(t, ms); !reflect.DeepEqual(got, want) {
		t.Fatalf("verdicts=%v, want %v", got, want)
	}
	verdict, err := catalog.New(catalog.WithArchitecture("GB200")).EvaluateAll(ms...)
	if err != nil || verdict.OverallSeverity != health.Unknown {
		t.Fatalf("verdict=%+v err=%v, want UNKNOWN", verdict, err)
	}
	for _, m := range ms {
		if health.KeyOf(m) == health.ProvisionalEntity("nvml", "1") {
			if failures := m.Errors(); len(failures) != 1 || !strings.Contains(failures[0].Err().Error(), "UUID read failed") {
				t.Fatalf("slot failures=%v, want original UUID error", failures)
			}
			return
		}
	}
	t.Fatal("no provisional inventory slot")
}

// TestPCIGPUAdapterMissingBusID keeps every inventory UUID represented, while
// preserving caller-provided GPU observations when no PCI join is possible.
func TestPCIGPUAdapterMissingBusID(t *testing.T) {
	for _, direct := range []int{0, 1, 2} {
		for _, inventoryError := range []bool{false, true} {
			t.Run(fmt.Sprintf("direct=%d/error=%v", direct, inventoryError), func(t *testing.T) {
				snapshot, err := (inventoryReader{gpus: 4, model: "NVIDIA GB200"}).Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				missing := snapshot.GetInventory().GetEntries()[3]
				missing.Gpu.BusId = nil
				detail := "no PCI bus ID"
				if inventoryError {
					detail = "original bus ID read failure"
					missing.FieldErrors = map[string]*observation.CollectionError{"bus_id": {Source: "nvml", Operation: "GetPciInfo", Category: observation.CollectionErrorCategoryPermissionDenied, Detail: detail}}
				}
				calls := 0
				adapter := pciGPUAdapter{CollectAdapter: collectadapters.PCIe(), lookup: func() *observation.GPUInventorySnapshot { calls++; return snapshot }}
				signals := collect.Signals{pcisignals.MaxLinkWidth, pcisignals.MaxLinkGeneration}
				var obs []*observation.Observation
				for slot := range 4 {
					for i, signal := range signals {
						value := uint64(16)
						if i == 1 {
							value = 5
						}
						target := observation.PCITarget(fmt.Sprintf("0000:%02x:00.0", slot+1))
						if slot == 3 && i < direct {
							target = observation.GPUTarget("GPU-3")
						}
						obs = append(obs, &observation.Observation{SignalId: signal.ID(), Source: "pci.sysfs", Unit: signal.Unit(), ObservedAt: timestamppb.Now(), Target: target, Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}}})
					}
				}
				ms := adapter.Build(adapter.prepare(obs))
				want := map[string]health.Severity{"GPU-0": health.Pass, "GPU-1": health.Pass, "GPU-2": health.Pass, "GPU-3": health.Unknown}
				if direct == 2 {
					want["GPU-3"] = health.Pass
				}
				if got := pciVerdicts(t, ms); !reflect.DeepEqual(got, want) || calls != 1 {
					t.Fatalf("results=%v want=%v lookups=%d", got, want, calls)
				}
				var verdicts []health.CheckVerdict
				for _, measurement := range ms {
					m := measurement.(pcie.PCIeMeasurement)
					v, err := catalog.New(catalog.WithArchitecture("GB200")).Evaluate("gpu_pcie_max_link", m)
					if err != nil {
						t.Fatal(err)
					}
					verdicts = append(verdicts, v)
					if m.GPUID == "GPU-3" && direct < 2 {
						if !strings.Contains(fmt.Sprint(v.Messages), detail) {
							t.Fatalf("inventory failure lost: %v", v.Messages)
						}
						if direct == 1 {
							if m.PCIeMaxLinkWidth != 16 || len(m.Errors()) != 1 || !slices.Equal(m.Errors()[0].FailedSignals(), []string{pcie.SignalPCIeMaxLinkGeneration}) {
								t.Fatalf("direct width changed: %+v errors=%v", m, m.Errors())
							}
						}
					}
				}
				if got := health.AggregateNodeVerdict(verdicts).OverallSeverity; got != want["GPU-3"] {
					t.Fatalf("overall=%v, want %v", got, want["GPU-3"])
				}
			})
		}
	}
}
