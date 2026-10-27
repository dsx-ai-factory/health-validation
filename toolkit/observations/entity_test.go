// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"slices"
	"strings"
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

// The collect command exports toolkit measurements and evaluation ingests
// them back. Each key survives that round trip as the producer set it: two
// sources' failed slot 0 stay distinct and keep their own source, and a
// whole-source failure stays source-level and targetless.
func TestRoundTripPreservesProvisionalAndSourceKeys(t *testing.T) {
	keyed := func(gpuID string, key health.EntityKey, err error) health.Measurement {
		m := gpu.ErroredGPU(gpuID, err, gpu.MeasurementSignals()...)
		m.SetEntityKey(key)
		return m
	}
	want := []health.EntityKey{
		health.ProvisionalEntity("nvml", "0"),
		health.ProvisionalEntity("smi", "0"),
		health.SourceEntity("smi"),
	}
	errs := []error{errors.New("nvml DeviceGetUUID failed"), errors.New("smi uuid unreadable"), errors.New("smi timed out")}
	original := []health.Measurement{
		keyed("nvml-index-0", want[0], errs[0]),
		keyed("smi-index-0", want[1], errs[1]),
		keyed("gpu", want[2], errs[2]),
	}

	exported, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), original...)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range exported {
		if o.GetSource() == "smi" && o.GetTarget() == nil {
			continue
		}
		if o.GetTarget().GetGpu().GetSourceLocalId() != "0" {
			t.Fatalf("%s from %s target = %v, want slot 0 or none", o.GetSignalId(), o.GetSource(), o.GetTarget())
		}
	}
	ms := ToMeasurements(exported)

	got := keysOf(ms)
	if !slices.Equal(slices.SortedFunc(slices.Values(got), compareKeys), slices.SortedFunc(slices.Values(want), compareKeys)) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, m := range ms {
		wantErr := errs[slices.Index(want, health.KeyOf(m))]
		if !slices.ContainsFunc(m.Errors(), func(e health.ErroredMeasurement) bool { return e.Err().Error() == wantErr.Error() }) {
			t.Errorf("%v errors = %v, want %q kept", health.KeyOf(m), m.Errors(), wantErr)
		}
	}

	nv, err := entityCatalog(t).EvaluateAll(ms...)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	critical := 0
	for _, v := range nv.CheckResults {
		if v.Severity == health.Critical {
			critical++
		}
	}
	if critical != len(want) {
		t.Fatalf("CRITICAL results = %d, want %d: %+v", critical, len(want), nv.CheckResults)
	}
}

func compareKeys(a, b health.EntityKey) int { return strings.Compare(a.String(), b.String()) }
