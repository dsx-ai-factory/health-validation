// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func entityCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	if err := cat.Register(gpu.GPUPowerState()); err != nil {
		t.Fatal(err)
	}
	return cat
}

// failedSlot is a UUID lookup failure on source's slot.
func failedSlot(source, slot string) []*observation.Observation {
	var out []*observation.Observation
	for _, o := range healthyGPUObservations() {
		f := failedObservation(o.GetSignalId(), &observation.CollectionError{Category: observation.CollectionErrorCategorySourceError})
		f.Source = source
		f.Target = observation.GPUSourceLocalTarget(slot)
		out = append(out, f)
	}
	return out
}

func keysOf(ms []health.Measurement) []health.EntityKey {
	keys := make([]health.EntityKey, len(ms))
	for i, m := range ms {
		keys[i] = health.KeyOf(m)
	}
	return keys
}

// Keys come from the typed target, and labels quote each part: source "a:b"
// slot "c" and source "a" slot "b:c" are different slots of different
// sources, which a "<source>:<slot>" label would render identically.
func TestToMeasurementsKeysEntitiesFromTypedTargets(t *testing.T) {
	observations := slices.Concat(failedSlot("a:b", "c"), failedSlot("a", "b:c"), healthyGPUObservations())
	ms := ToMeasurements(observations)
	var labels []string
	for _, m := range ms {
		labels = append(labels, m.(gpu.GPUMeasurement).GPUID)
	}
	if want := []string{"GPU-0", `slot:"a"/"b:c"`, `slot:"a:b"/"c"`}; !slices.Equal(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	got := keysOf(ms)
	want := []health.EntityKey{
		health.VerifiedEntity("GPU-0"),
		health.ProvisionalEntity("a", "b:c"),
		health.ProvisionalEntity("a:b", "c"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

// Two sources' failed slot 0 are two unresolved GPUs: each fails its checks,
// and neither is a duplicate of the other.
func TestToMeasurementsSlotsFromTwoSourcesEvaluateSeparately(t *testing.T) {
	ms := ToMeasurements(slices.Concat(failedSlot("nvml", "0"), failedSlot("dcgm", "0")))
	nv, err := entityCatalog(t).EvaluateAll(ms...)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	var entities []string
	for _, v := range nv.CheckResults {
		if v.Severity != health.Critical {
			t.Errorf("%v severity = %v, want CRITICAL", v.AffectedEntities, v.Severity)
		}
		entities = append(entities, v.AffectedEntities...)
	}
	if !slices.Equal(entities, []string{`slot:"dcgm"/"0"`, `slot:"nvml"/"0"`}) {
		t.Fatalf("entities = %v, want one failure per source slot", entities)
	}
}

// Within one batch, a slot reported twice is caught as a duplicate
// observation. Across batches, as when two adapters each convert one, it
// reaches the catalog's check/entity guard.
func TestToMeasurementsDuplicateSlotAcrossBatchesIsAmbiguous(t *testing.T) {
	ms := slices.Concat(ToMeasurements(failedSlot("nvml", "0")), ToMeasurements(failedSlot("nvml", "0")))
	if _, err := entityCatalog(t).EvaluateAll(ms...); !errors.Is(err, health.ErrAmbiguousMeasurement) {
		t.Fatalf("EvaluateAll err = %v, want ErrAmbiguousMeasurement", err)
	}
}

// A slot with no source cannot be scoped, so evaluation refuses it rather
// than letting it stand for any source's slot.
func TestToMeasurementsSlotWithoutSourceFailsClosed(t *testing.T) {
	ms := ToMeasurements(failedSlot("", "0"))
	if got, want := keysOf(ms), []health.EntityKey{health.ProvisionalEntity("", "0")}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if _, err := entityCatalog(t).EvaluateAll(ms...); !errors.Is(err, health.ErrAmbiguousMeasurement) {
		t.Fatalf("EvaluateAll err = %v, want ErrAmbiguousMeasurement", err)
	}
}

// Source-local slots carrying values are not attributable to a verified GPU,
// so evaluation rejects them rather than evaluating an unjoined GPU.
func TestToMeasurementsSlotValuesAreRejected(t *testing.T) {
	observations := healthyGPUObservations()
	for _, o := range observations {
		o.Target = observation.GPUSourceLocalTarget("0")
	}
	if _, err := entityCatalog(t).EvaluateAll(ToMeasurements(observations)...); !errors.Is(err, health.ErrAmbiguousMeasurement) {
		t.Fatalf("EvaluateAll err = %v, want ErrAmbiguousMeasurement", err)
	}
}

// A source-level GPU failure is scoped to its source, not merged across
// sources into one unidentified GPU.
func TestToMeasurementsSourceLevelFailuresStayPerSource(t *testing.T) {
	var observations []*observation.Observation
	for _, source := range []string{"dcgm", "nvml"} {
		o := failedObservation(gpusignals.PerformanceState.ID(), &observation.CollectionError{Category: observation.CollectionErrorCategoryTimeout})
		o.Source = source
		o.Target = nil
		observations = append(observations, o)
	}
	want := []health.EntityKey{health.SourceEntity("dcgm"), health.SourceEntity("nvml")}
	if got := keysOf(ToMeasurements(observations)); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

// A run evaluates one node, so host-wide observations form one measurement
// whatever node ID each target carries.
func TestToMeasurementsHostWideIgnoresNodeID(t *testing.T) {
	var observations []*observation.Observation
	for i, s := range []string{cpusignals.PossibleCount.ID(), cpusignals.OnlineCount.ID(), cpusignals.NUMANodeOnlineCount.ID()} {
		o := valueObservation(s, intValue(4))
		o.Unit = cpusignals.PossibleCount.Unit()
		o.Target = observation.NodeTarget([]string{"", "node-a", "node-b"}[i])
		observations = append(observations, o)
	}
	m := only[cpu.CPUMeasurement](t, ToMeasurements(observations))
	if health.KeyOf(m) != health.NodeEntity() || len(mappedErrors(m)) != 0 {
		t.Fatalf("CPU measurement %+v keyed %v, errors %v", m, health.KeyOf(m), m.Errors())
	}
}

// The CLI exports toolkit measurements and ingests them back. Each failed
// NVML slot keeps its own provisional key and gpu_id through that round trip,
// rather than collapsing into one source-level failure, and a whole-source
// failure stays targetless rather than becoming a GPU slot.
func TestRoundTripKeepsProvisionalSlotsDistinct(t *testing.T) {
	slot := func(index string) health.Measurement {
		m := gpu.ErroredGPU("nvml-index-"+index, errors.New("DeviceGetUUID failed"), gpu.MeasurementSignals()...)
		m.SetEntityKey(health.ProvisionalEntity("nvml", index))
		return m
	}
	exported, err := FromMeasurements("gpu", testNode, time.Unix(1, 0),
		slot("0"), slot("1"), gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0})
	if err != nil {
		t.Fatal(err)
	}
	ms := ToMeasurements(exported)

	want := []health.EntityKey{
		health.VerifiedEntity("GPU-0"),
		health.ProvisionalEntity("toolkit.gpu", "nvml-index-0"),
		health.ProvisionalEntity("toolkit.gpu", "nvml-index-1"),
	}
	if got := keysOf(ms); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}

	whole := gpu.ErroredGPU("gpu", errors.New("sensor timed out"), gpu.MeasurementSignals()...)
	whole.SetEntityKey(health.SourceEntity("toolkit.gpu"))
	exportedWhole, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), whole)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range exportedWhole {
		if o.GetTarget() != nil {
			t.Fatalf("%s target = %v, want none for a source-level failure", o.GetSignalId(), o.GetTarget())
		}
	}
	if got, want := keysOf(ToMeasurements(exportedWhole)), []health.EntityKey{health.SourceEntity("toolkit.gpu")}; !slices.Equal(got, want) {
		t.Fatalf("source-level keys = %v, want %v", got, want)
	}
	nv, err := entityCatalog(t).EvaluateAll(ms...)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	var failed []string
	for _, v := range nv.CheckResults {
		if v.Severity == health.Critical {
			failed = append(failed, v.AffectedEntities...)
		}
	}
	if !slices.Equal(failed, []string{"nvml-index-0", "nvml-index-1"}) {
		t.Fatalf("failed entities = %v, want each slot by its original gpu_id", failed)
	}
}
