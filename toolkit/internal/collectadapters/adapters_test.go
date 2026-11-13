// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDomainAdaptersPreserveExistingMappings(t *testing.T) {
	obs := append(healthyGPUObservations(), healthyNVLinkObservations()...)
	for _, a := range []*domainAdapter{GPU(), NVLink()} {
		t.Run(a.Name(), func(t *testing.T) {
			category := health.GPU
			if a.Name() == "nvlink" {
				category = health.NVLink
			}
			var expected []health.Measurement
			for _, m := range observations.ToMeasurements(obs) {
				if m.Category() == category {
					expected = append(expected, m)
				}
			}
			got := a.Build(obs)
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("wrapper changed mappings:\n got=%+v\n want=%+v", got, expected)
			}
			empty := a.Build(nil)
			if len(empty) != 1 || empty[0].Category() != category || len(empty[0].Errors()) == 0 {
				t.Fatalf("empty input omitted domain failure: %v", empty)
			}
		})
	}
}

// Endpoint and pair observations group with the GPU they belong to; a
// per-source group would merge every GPU's links and stamp the source label
// as their GPU ID.
func TestDomainAdaptersNamespaceFailedSourceLocalSlots(t *testing.T) {
	first := failedObservation(gpusignals.PerformanceState.ID(), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable})
	first.Source = "a:b"
	first.Target = observation.GPUSourceLocalTarget("c")
	second := failedObservation(gpusignals.PerformanceState.ID(), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable})
	second.Source = "a"
	second.Target = observation.GPUSourceLocalTarget("b:c")
	got := GPU().Build([]*observation.Observation{first, second})
	if len(got) != 2 {
		t.Fatalf("source/slot identities collapsed: %v", got)
	}
	ids := map[string]bool{}
	for _, m := range got {
		if m.Category() != health.GPU {
			t.Fatal("foreign domain emitted")
		}
		if len(m.Errors()) == 0 {
			t.Fatal("failed slot became healthy")
		}
	}
	// Two different tuple components must remain distinct in display identity.
	for _, m := range got {
		field := m.(gpu.GPUMeasurement).GPUID
		if ids[field] {
			t.Fatalf("display identity collision: %q", field)
		}
		ids[field] = true
	}
	// Evaluation keys them from the typed target, so they stay distinct
	// whatever their labels.
	keys := []health.EntityKey{health.KeyOf(got[0]), health.KeyOf(got[1])}
	if want := []health.EntityKey{health.ProvisionalEntity("a", "b:c"), health.ProvisionalEntity("a:b", "c")}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

// A whole-domain failure names no GPU, so it is keyed by its source rather
// than taking its "gpu" label as a GPU identity.
func TestDomainAdaptersKeyDomainFailureBySource(t *testing.T) {
	for _, a := range []*domainAdapter{GPU(), NVLink()} {
		got := a.Errored(errors.New("unavailable"))
		if len(got) != 1 || health.KeyOf(got[0]) != health.SourceEntity(adapterSource(a.Category())) {
			t.Fatalf("%s: Errored = %v keyed %v, want one source-level measurement", a.Name(), got, health.KeyOf(got[0]))
		}
	}
	if got := CPU().Errored(errors.New("unavailable")); health.KeyOf(got[0]) != health.NodeEntity() {
		t.Fatalf("cpu: Errored keyed %v, want the node", health.KeyOf(got[0]))
	}
}

// A timeout reported by DCGM and NVML keeps one measurement per source, and
// each fails the other source's fields as a timeout rather than as never
// collected.
func TestNVLinkAdapterSourceLevelFailuresCoverEachOther(t *testing.T) {
	timeout := &observation.CollectionError{Category: observation.CollectionErrorCategoryTimeout}
	dcgm := failedObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), timeout)
	nvml := failedObservation(gpusignals.NVLinkLinkCount.ID(), timeout)
	dcgm.Target, nvml.Target, nvml.Source = nil, nil, "nvml"
	got := NVLink().Build([]*observation.Observation{dcgm, nvml})
	keys := []health.EntityKey{health.SourceEntity(dcgm.GetSource()), health.SourceEntity("nvml")}
	if len(got) != 2 || health.KeyOf(got[0]) != keys[0] || health.KeyOf(got[1]) != keys[1] {
		t.Fatalf("Build = %v, want one measurement per source", got)
	}
	for _, m := range got {
		for _, field := range []string{nvlink.SignalFabricAccessTimeoutRecovery, nvlink.SignalLinkCount} {
			timedOut := false
			for _, e := range m.Errors() {
				if slices.Contains(e.FailedSignals(), field) {
					timedOut = errors.Is(e.Err(), health.ErrCollectionTimeout)
				}
			}
			if !timedOut {
				t.Errorf("%v: %s is not a timeout: %v", health.KeyOf(m), field, m.Errors())
			}
		}
	}
}

// With no GPUs, NVML and DCGM report NO_TARGETS for every NVLink signal. The
// adapter must fail every NVLink check as a collection failure rather than
// let the domain pass or drop out as a skip.
func TestNVLinkAdapterWithoutGPUsFailsEveryCheck(t *testing.T) {
	var noTargets []*observation.Observation
	for id := range NVLink().Signals() {
		o := noValueObservation(id, observation.NoValueReasonNoTargets)
		o.Target = nil
		noTargets = append(noTargets, o)
	}
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	category := health.NVLink
	checks := cat.List(catalog.Filter{Category: &category, Architecture: "GB200"})
	want := health.Drain(health.RemediationActionCheckCollection)
	for name, obs := range map[string][]*observation.Observation{"no observations": nil, "no targets": noTargets} {
		t.Run(name, func(t *testing.T) {
			verdict, err := cat.EvaluateAll(NVLink().Build(obs)...)
			if err != nil {
				t.Fatal(err)
			}
			got := 0
			for _, r := range verdict.CheckResults {
				if r.Category != health.NVLink {
					continue
				}
				got++
				if r.Severity != health.Unknown || !r.RecommendedAction.Equal(want) {
					t.Errorf("%s: %s %v, want UNKNOWN %v", r.CheckID, r.Severity, r.RecommendedAction, want)
				}
			}
			if len(checks) == 0 || got != len(checks) {
				t.Fatalf("evaluated %d NVLink checks, want all %d", got, len(checks))
			}
		})
	}
}

func TestGPUAdapterPreservesAbsenceInterpretation(t *testing.T) {
	baseline := only[gpu.GPUMeasurement](t, GPU().Build(healthyGPUObservations()))
	neutral := replace(healthyGPUObservations(), gpusignals.HardwareSlowdownActive.ID(), noValueObservation(gpusignals.HardwareSlowdownActive.ID(), observation.NoValueReasonNotSupported))
	if m := only[gpu.GPUMeasurement](t, GPU().Build(neutral)); len(m.Errors()) != len(baseline.Errors()) {
		t.Fatalf("unsupported neutral fact became failed: %v", m.Errors())
	}
	noTargets := noValueObservation(gpusignals.PerformanceState.ID(), observation.NoValueReasonNoTargets)
	noTargets.Target = nil
	tests := []struct {
		name         string
		failedSignal string
		obs          []*observation.Observation
	}{
		{name: "missing", failedSignal: gpu.SignalAveragePowerDrawW, obs: healthyGPUObservations()[1:]},
		{name: "collection error", failedSignal: gpu.SignalGPUPstate, obs: replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(), failedObservation(gpusignals.PerformanceState.ID(), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}))},
		{name: "no targets", failedSignal: gpu.SignalGPUPstate, obs: replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(), noTargets)},
		// Toolkit-shaped IDs are treated as neutral when unsupported; only
		// FromMeasurements output may use them, so collect input must not.
		{name: "toolkit-shaped unsupported", failedSignal: gpu.SignalGPUPstate, obs: replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(), noValueObservation("gpu."+gpu.SignalGPUPstate, observation.NoValueReasonNotSupported))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GPU().Build(tt.obs)
			if len(got) == 0 {
				t.Fatal("absence omitted the domain")
			}
			for _, m := range got {
				var failed bool
				for _, failure := range m.Errors() {
					failed = failed || slices.Contains(failure.FailedSignals(), tt.failedSignal)
				}
				if !failed {
					t.Fatalf("absence did not fail %q: %+v", tt.failedSignal, m)
				}
			}
		})
	}
}

func healthyGPUObservations() []*observation.Observation {
	return []*observation.Observation{
		valueObservation(gpusignals.PowerDraw.ID(), doubleValue(75)),
		valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700)),
		valueObservation(gpusignals.PowerLimitEnforced.ID(), doubleValue(700)),
		valueObservation(gpusignals.PerformanceState.ID(), intValue(0)),
		valueObservation(gpusignals.HardwareSlowdownActive.ID(), boolValue(false)),
		valueObservation(gpusignals.HardwareThermalSlowdownActive.ID(), boolValue(true)),
		valueObservation(gpusignals.HardwarePowerBrakeSlowdownActive.ID(), boolValue(false)),
	}
}

func healthyNVLinkObservations() []*observation.Observation {
	return []*observation.Observation{
		valueObservation(gpusignals.NVLinkFabricBandwidthDegraded.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricRouteRecoveryActive.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), boolValue(false)),
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess)),
	}
}

// replace returns observations with signalID's observation swapped for with.
func replace(observations []*observation.Observation, signalID string, with *observation.Observation) []*observation.Observation {
	for i, o := range observations {
		if o.GetSignalId() == signalID {
			observations[i] = with
		}
	}
	return observations
}

func valueObservation(signalID string, value *observation.Value) *observation.Observation {
	o := failedObservation(signalID, nil)
	o.Outcome = &observation.Observation_Value{Value: value}
	return o
}

func failedObservation(signalID string, failure *observation.CollectionError) *observation.Observation {
	var unit *string
	for _, s := range gpusignals.All() {
		if s.ID() == signalID {
			unit = s.Unit()
		}
	}
	return &observation.Observation{
		SignalId:   signalID,
		Source:     "dcgm",
		Target:     observation.GPUTarget("GPU-0"),
		ObservedAt: timestamppb.Now(),
		Unit:       unit,
		Outcome:    &observation.Observation_CollectionError{CollectionError: failure},
	}
}

func noValueObservation(signalID string, reason observation.NoValueReason) *observation.Observation {
	o := failedObservation(signalID, nil)
	o.Outcome = &observation.Observation_NoValue{NoValue: &observation.NoValue{Reason: reason}}
	return o
}

// only asserts measurements holds exactly one M and returns it.
func only[M health.Measurement](t *testing.T, measurements []health.Measurement) M {
	t.Helper()
	var found []M
	for _, m := range measurements {
		if typed, ok := m.(M); ok {
			found = append(found, typed)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %T, got %d", *new(M), len(found))
	}
	return found[0]
}

func intValue(v int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}
}

func doubleValue(v float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: v}}
}

func boolValue(v bool) *observation.Value {
	return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}}
}

// Two sources feeding the node-scoped CPU domain yield one measurement, also
// when one of them failed as a whole.
func TestCPUAdapterMergesSources(t *testing.T) {
	observe := func(signal collect.Signal, source string, value int64) *observation.Observation {
		return &observation.Observation{
			SignalId: signal.ID(), Source: source, Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
			Unit: signal.Unit(), Outcome: &observation.Observation_Value{Value: intValue(value)},
		}
	}
	numa := cpusignals.NUMANodeOnlineCount
	tests := []struct {
		numa   *observation.Observation
		name   string
		failed []string
		want   int64
	}{
		{name: "values", numa: observe(numa, "custom", 2), want: 2},
		{
			name:   "source-scoped failure",
			numa:   observation.NewErrorObservation(numa.ID(), numa.Unit(), "custom", "Measure", errors.New("unavailable")),
			failed: []string{cpu.SignalNUMANodeCount},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := only[cpu.CPUMeasurement](t, CPU().Build([]*observation.Observation{
				observe(cpusignals.PossibleCount, "cpu.sysfs", 8),
				observe(cpusignals.OnlineCount, "cpu.sysfs", 8),
				tt.numa,
			}))
			var failed []string
			for _, e := range got.Errors() {
				failed = append(failed, e.FailedSignals()...)
			}
			if health.KeyOf(got) != health.NodeEntity() {
				t.Fatalf("CPU measurement keyed %v, want the node", health.KeyOf(got))
			}
			if got.PossibleCPUCount != 8 || got.OnlineCPUCount != 8 || got.NUMANodeCount != tt.want || !slices.Equal(failed, tt.failed) {
				t.Fatalf("got %+v, failed %v; want counts 8/8/%d, failed %v", got, failed, tt.want, tt.failed)
			}
		})
	}
}

func TestAdapterRequestsMatchConverterMappings(t *testing.T) {
	var got []string
	for _, adapter := range []*domainAdapter{GPU(), NVLink(), CPU(), Node(), XID(nil).domainAdapter, InfiniBand()} {
		got = slices.AppendSeq(got, maps.Keys(adapter.Signals()))
	}
	want := observations.MappedSignals()
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter signals=%v, converter signals=%v", got, want)
	}
}

func nodeCountObservation(source string, count int64) *observation.Observation {
	return &observation.Observation{
		SignalId:   gpuinventory.GPUCount.ID(),
		Source:     source,
		Target:     observation.NodeTarget(""),
		ObservedAt: timestamppb.Now(),
		Unit:       gpuinventory.GPUCount.Unit(),
		Outcome:    &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: count}}},
	}
}

func TestNodeAdapterBuildsGPUCount(t *testing.T) {
	a := Node()
	if a.Name() != "node" || a.Category() != health.Node {
		t.Fatalf("name=%q category=%v, want the node category", a.Name(), a.Category())
	}
	m := only[node.NodeMeasurement](t, a.Build([]*observation.Observation{nodeCountObservation(gpuinventory.SensorID, 4)}))
	if m.GPUCount != 4 || len(m.Errors()) != 0 {
		t.Fatalf("measurement=%+v errors=%v", m, m.Errors())
	}
	empty := only[node.NodeMeasurement](t, a.Build(nil))
	if len(empty.Errors()) == 0 || !slices.Contains(empty.Errors()[0].FailedSignals(), node.SignalGPUCount) {
		t.Fatalf("empty input did not fail gpu_count: %v", empty.Errors())
	}
}

// A successful inventory read that finds no GPUs is a count of zero that the
// rule judges; a failed or absent read is a collection failure.
func TestNodeAdapterGPUCountVerdicts(t *testing.T) {
	failed := observation.NewCollectionErrorObservation(gpuinventory.GPUCount.ID(), gpuinventory.GPUCount.Unit(), gpuinventory.SensorID, "GPUInventory", nil,
		timestamppb.Now(), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, SourceError: "ERROR_UNINITIALIZED"})
	collectionFailure := health.Drain(health.RemediationActionCheckCollection)
	for _, tt := range []struct {
		name  string
		obs   []*observation.Observation
		ruled bool
	}{
		{name: "successful zero", obs: []*observation.Observation{nodeCountObservation(gpuinventory.SensorID, 0)}, ruled: true},
		{name: "failed enumeration", obs: []*observation.Observation{failed}},
		{name: "no observations"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
			cat.MustRegister(node.GPUCount())
			nv, err := cat.EvaluateAll(Node().Build(tt.obs)...)
			if err != nil {
				t.Fatal(err)
			}
			want := health.Unknown
			if tt.ruled {
				want = health.Critical
			}
			if len(nv.CheckResults) != 1 || nv.CheckResults[0].Severity != want {
				t.Fatalf("results = %+v, want one %v gpu_count", nv.CheckResults, want)
			}
			v := nv.CheckResults[0]
			if tt.ruled {
				if v.ObservedValues[node.SignalGPUCount] != int64(0) || !v.RecommendedAction.Equal(node.GPUCount().RecommendedAction) {
					t.Fatalf("verdict = %+v, want the rule to judge a count of 0", v)
				}
				return
			}
			if v.ObservedValues != nil || len(v.Messages) != 1 || !strings.HasPrefix(v.Messages[0], "measurement unavailable: ") ||
				!v.RecommendedAction.Equal(collectionFailure) {
				t.Fatalf("verdict = %+v, want a collection failure", v)
			}
		})
	}
}

// A count from one source and another source's source-scoped failure form one
// node, not one per source, so the failure cannot be dropped as a second host.
func TestNodeAdapterAssemblesSourcesIntoOneMeasurement(t *testing.T) {
	failure := observation.NewCollectionErrorObservation(gpuinventory.GPUCount.ID(), gpuinventory.GPUCount.Unit(), "other", "GPUInventory", nil,
		timestamppb.Now(), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable})
	got := Node().Build([]*observation.Observation{nodeCountObservation(gpuinventory.SensorID, 4), failure})
	m := only[node.NodeMeasurement](t, got)
	if len(got) != 1 || len(m.Errors()) == 0 || !slices.Contains(m.Errors()[0].FailedSignals(), node.SignalGPUCount) {
		t.Fatalf("measurements=%+v", got)
	}
}
