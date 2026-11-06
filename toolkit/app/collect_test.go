// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
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
		{name: "default", app: New(), sources: []string{"test.gpu", cpusensor.ID, gpuinventory.SensorID, xidsensor.ID}, adapters: []string{"gpu", "cpu", "node", "xid"}},
		{name: "GPU override", app: New().WithGPUBackend(spyBackend{called: new(bool)}), sources: []string{cpusensor.ID, gpuinventory.SensorID, xidsensor.ID}, adapters: []string{"cpu", "node", "xid"}},
		{name: "CPU override", app: New().WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{})), sources: []string{"test.gpu", gpuinventory.SensorID, xidsensor.ID}, adapters: []string{"gpu", "node", "xid"}},
		{name: "node override", app: New().WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{})), sources: []string{"test.gpu", cpusensor.ID, xidsensor.ID}, adapters: []string{"gpu", "cpu", "xid"}},
		{name: "Xid override", app: New().WithXidBackend(xidSpyBackend{called: new(bool)}), sources: []string{"test.gpu", cpusensor.ID, gpuinventory.SensorID}, adapters: []string{"gpu", "cpu", "node"}},
	}
	for _, test := range tests {
		sources, adapters := builtins(test.app)
		if !slices.Equal(sources, test.sources) || !slices.Equal(adapters, test.adapters) {
			t.Errorf("%s: built-ins = %v %v, want %v %v", test.name, sources, adapters, test.sources, test.adapters)
		}
	}
}

// Production collection goes through collect sensors. Only domains not yet
// migrated may still run a legacy toolkit sensor by default; shrink this list
// as each one moves, and do not grow it.
func TestDefaultLegacySensorsAreOnlyUnmigratedDomains(t *testing.T) {
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
	legacyPkg := reflect.TypeFor[toolkit.NVLinkSensor]().PkgPath()
	var legacy []string
	for _, s := range sensors {
		if typ := reflect.TypeOf(s); typ.Kind() == reflect.Pointer && typ.Elem().PkgPath() == legacyPkg {
			legacy = append(legacy, typ.Elem().Name())
		}
	}
	if want := []string{"NVLinkSensor", "RDMALinkFlapSensor"}; !slices.Equal(legacy, want) {
		t.Fatalf("default legacy sensors = %v, want %v; new production domains must use collect sensors", legacy, want)
	}
}

func TestCollectSkipsUnselectedBridgeSources(t *testing.T) {
	source := &collectAppSensor{}
	a := New().WithCollect(source, claimAll(source)...).WithCollectAdapters(customCountAdapter())
	builtinSources, builtinAdapters := a.builtinCollect(nil)
	sensors, err := a.collectSensors(time.Second, builtinSources, builtinAdapters, requiredSignals{health.NVLink: nil})
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

func TestLegacyOnlyCollectionIsUnchangedByCollectPlumbing(t *testing.T) {
	a := cadenceRunApp().WithNodeID(testNodeID).
		WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{GPUCount: 4}))
	cpuCalls := 0
	a.WithCPUBackend(cpuBackendFunc(func(context.Context) (cpu.CPUMeasurement, error) {
		cpuCalls++
		return cpu.CPUMeasurement{PossibleCPUCount: 4, OnlineCPUCount: 3, NUMANodeCount: 34}, nil
	}))
	bridged, err := a.collectSensors(time.Second, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridged) != 0 {
		t.Fatalf("unregistered collect sensors added %d bridges", len(bridged))
	}
	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("runCommand exit=%d, want %d; stderr=%q", code, exitUnhealthy, stderr.String())
	}
	if cpuCalls != 1 || !*a.host.(fakeHost).b.called {
		t.Fatalf("legacy providers were not called: cpu=%d gpu=%v", cpuCalls, *a.host.(fakeHost).b.called)
	}
	var verdict health.NodeVerdict
	if err = json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	results := make(map[string]health.CheckVerdict)
	for _, result := range verdict.CheckResults {
		results[result.CheckID] = result
	}
	if result, ok := results["cpu_count"]; !ok || result.Severity != health.Critical || result.ObservedValues[cpu.SignalOnlineCPUCount] != float64(3) {
		t.Fatalf("legacy CPU failure did not reach the verdict: %+v", verdict)
	}
	if result, ok := results["gpu_count"]; !ok || result.Severity != health.Pass || result.ObservedValues[node.SignalGPUCount] != float64(4) {
		t.Fatalf("legacy node measurement did not reach the verdict: %+v", verdict)
	}
	if verdict.NodeID != testNodeID {
		t.Fatalf("node ID=%q, want %q", verdict.NodeID, testNodeID)
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
			legacyCalls := 0
			measurement := cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}
			a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter())
			a.WithCPUBackend(cpuBackendFunc(func(context.Context) (cpu.CPUMeasurement, error) {
				legacyCalls++
				return measurement, nil
			}))
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
			if legacyCalls != 1 || len(sensor.cycles) != wantCollect {
				t.Fatalf("legacy calls=%d collect calls=%d, want 1 and %d", legacyCalls, len(sensor.cycles), wantCollect)
			}
		})
	}
}

func TestWithCollectCreatesFreshCycleOnRepeatedRuns(t *testing.T) {
	sensor := &collectAppSensor{}
	a := cadenceRunApp().WithNodeID(testNodeID).WithChecks(mixedChecks()...).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(customCountAdapter())
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
	for _, name := range []string{"nil sensor", "typed nil sensor", "nil signal", "no adapter", "nil adapter", "typed nil adapter", "lazy typed nil adapter", "incomplete adapter", "unowned signal", "duplicate producer", "duplicate adapter", "duplicate adapter across calls", "built-in adapter name", "built-in xid adapter name"} {
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
			case "no adapter":
				a.WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{})).WithGPUBackend(spyBackend{called: new(bool)}).WithCollect(sensor, claimAll(sensor)...)
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
				a.cpu = nil
				adapter := customCountAdapter()
				adapter.name = "cpu"
				a.WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
			case "built-in xid adapter name":
				a.xid = nil
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
			if name == "no adapter" && !strings.Contains(stderr.String(), "collect sensors registered without adapters; register adapters with WithCollectAdapters") {
				t.Fatalf("missing adapters were not explained: %q", stderr.String())
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
		{name: "gpu"},    // built-in active
		{name: "nvlink"}, // domain still on its legacy sensor
		{name: "xid", configure: func(a *App) { a.WithXidBackend(xidSpyBackend{called: new(bool)}) }},
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
	sensors = append(sensors, toolkit.NewCPUSensor(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1})))
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
