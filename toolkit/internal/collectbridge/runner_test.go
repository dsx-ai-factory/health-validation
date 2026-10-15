// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectbridge_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// runnerSource is a collect.Sensor whose Measure is supplied per test.
type runnerSource struct {
	measure func(context.Context, *collect.Op) (*observation.ObservationBatch, error)
	id      string
	signals collect.Signals
}

func (s runnerSource) ID() string               { return s.id }
func (s runnerSource) Signals() collect.Signals { return s.signals }
func (s runnerSource) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	return s.measure(ctx, collect.NewOp(opts...))
}

var runnerCount = collect.NewSignal("private.count", "1")

func newCollectRunner(t *testing.T, timeout time.Duration) *collectbridge.Runner {
	t.Helper()
	cycle, err := collect.NewCycleWithTimeout("run-1", time.Now(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	return collectbridge.NewRunner(cycle)
}

func runnerObservation(s collect.Signal, target *observation.ObservationTarget, v int64) *observation.Observation {
	return &observation.Observation{SignalId: s.ID(), Source: "fake", Target: target, ObservedAt: timestamppb.Now(), Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}}}
}

func runnerBatch(obs ...*observation.Observation) func(context.Context, *collect.Op) (*observation.ObservationBatch, error) {
	return func(_ context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
		return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID(), Observations: obs}, nil
	}
}

func registerRunner(t *testing.T, r *collectbridge.Runner, s runnerSource) {
	t.Helper()
	if err := r.Register(s, s.signals.IDs()...); err != nil {
		t.Fatal(err)
	}
}

// countAdapter sums count observations into a node GPU count and records
// failures and empty collections as errors.
func countAdapter(signals ...string) collectbridge.Binding {
	return collectbridge.Binding{Name: "node", Signals: signals, Build: func(obs []*observation.Observation) []health.Measurement {
		m := node.NodeMeasurement{}
		if len(obs) == 0 {
			m.AddError(errors.New("empty collection"), node.SignalGPUCount)
		}
		for _, o := range obs {
			if failure := o.GetCollectionError(); failure != nil {
				m.AddError(errors.New(failure.GetDetail()), node.SignalGPUCount)
			} else if o.GetValue() == nil {
				m.AddError(errors.New("missing count"), node.SignalGPUCount)
			} else {
				m.GPUCount += o.GetValue().GetIntValue()
			}
		}
		return []health.Measurement{m}
	}, Errored: func(err error) []health.Measurement {
		return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
	}}
}

func TestCollectRunnerMixedLegacyAndSharedCycle(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	other := collect.NewSignal("private.other", "1")
	var cycles []*collect.Cycle
	var selections [][]string
	for _, s := range []collect.Signal{runnerCount, other} {
		current := s
		registerRunner(t, r, runnerSource{id: s.ID(), signals: collect.Signals{s}, measure: func(_ context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
			cycles = append(cycles, op.Cycle())
			selected, err := op.Signals(collect.Signals{current})
			if err != nil {
				t.Fatal(err)
			}
			selections = append(selections, selected.IDs())
			return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID(), Observations: []*observation.Observation{runnerObservation(current, observation.NodeTarget(""), 2)}}, nil
		}})
	}
	r.Collect()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	if err := cat.Register(node.GPUCount()); err != nil {
		t.Fatal(err)
	}
	if err := cat.Register(cpu.CPUCount()); err != nil {
		t.Fatal(err)
	}
	legacy := toolkit.NewCPUSensor(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 8}))
	verdict, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: time.Second}, r.Sensor(countAdapter(runnerCount.ID(), other.ID())), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.CheckResults) != 2 {
		t.Fatalf("verdict=%+v", verdict)
	}
	for _, v := range verdict.CheckResults {
		if v.Severity != health.Pass {
			t.Fatalf("check %s=%s", v.CheckID, v.Severity)
		}
	}
	r.Observe(runnerCount.ID(), other.ID())
	r.Observe(other.ID())
	if len(cycles) != 2 || cycles[0] != cycles[1] || cycles[0].CollectionID() != "run-1" || len(selections) != 2 {
		t.Fatalf("cycles=%v selections=%v", cycles, selections)
	}
}

func TestCollectRunnerExplicitOwnershipAndSelection(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	other := collect.NewSignal("private.other", "1")
	s := runnerSource{id: "first", signals: collect.Signals{runnerCount, other}, measure: runnerBatch()}
	if err := r.Register(s, runnerCount.ID()); err != nil {
		t.Fatal(err)
	}
	s.id = "second"
	if err := r.Register(s, runnerCount.ID()); err == nil || !strings.Contains(err.Error(), "select one source") {
		t.Fatalf("overlap=%v", err)
	}
	s.id = "unknown"
	if err := r.Register(s, "not-declared"); err == nil {
		t.Fatal("unknown requested signal accepted")
	}
	r2 := newCollectRunner(t, time.Second)
	if err := r2.Register(runnerSource{id: "wrong", signals: collect.Signals{collect.NewSignal(gpusignals.PowerDraw.ID(), "bytes")}, measure: runnerBatch()}, gpusignals.PowerDraw.ID()); err == nil {
		t.Fatal("canonical unit redefinition accepted")
	}
	obs := r.Observe(gpusignals.PowerDraw.ID())
	if len(obs) != 1 || obs[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryNotConfigured || obs[0].GetUnit() != "watts" {
		t.Fatalf("unknown owner=%v", obs)
	}
}

func TestCollectRunnerRejectsNilDeclarationsBeforeSelection(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	s := runnerSource{id: "nil", signals: collect.Signals{runnerCount, nil}, measure: runnerBatch()}
	for _, selection := range [][]string{{runnerCount.ID()}, {runnerCount.ID(), "other"}} {
		if err := r.Register(s, selection...); err == nil || !strings.Contains(err.Error(), "nil signal") {
			t.Fatalf("selection=%v: Register() error=%v, want nil signal error", selection, err)
		}
	}
	s.signals = collect.Signals{runnerCount}
	if err := r.Register(s, s.signals.IDs()...); err != nil {
		t.Fatalf("rejected registration retained ownership: %v", err)
	}
}

func TestCollectRunnerRejectsMalformedProducerBatches(t *testing.T) {
	tests := []struct {
		mutate func(*observation.ObservationBatch)
		name   string
	}{
		{name: "foreign cycle", mutate: func(b *observation.ObservationBatch) { b.CollectionId = "foreign" }},
		{name: "missing cycle", mutate: func(b *observation.ObservationBatch) { b.CollectionId = "" }},
		{name: "unowned signal", mutate: func(b *observation.ObservationBatch) {
			b.Observations = append(b.Observations, runnerObservation(collect.NewSignal("other", "1"), observation.NodeTarget(""), 1))
		}},
		{name: "empty source", mutate: func(b *observation.ObservationBatch) { b.Observations[0].Source = "" }},
		{name: "nil observation", mutate: func(b *observation.ObservationBatch) { b.Observations = append(b.Observations, nil) }},
		{name: "empty value kind", mutate: func(b *observation.ObservationBatch) { b.Observations[0].GetValue().Kind = nil }},
		{name: "typed nil int kind", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].GetValue().Kind = (*observation.Value_IntValue)(nil)
		}},
		{name: "typed nil double kind", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].GetValue().Kind = (*observation.Value_DoubleValue)(nil)
		}},
		{name: "nil value payload", mutate: func(b *observation.ObservationBatch) { b.Observations[0].Outcome = &observation.Observation_Value{} }},
		{name: "future timestamp", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].ObservedAt = timestamppb.New(time.Now().Add(time.Hour))
		}},
		{name: "invalid timestamp", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].ObservedAt = &timestamppb.Timestamp{Seconds: 1, Nanos: -1}
		}},
		{name: "missing timestamp", mutate: func(b *observation.ObservationBatch) { b.Observations[0].ObservedAt = nil }},
		{name: "invalid GPU target", mutate: func(b *observation.ObservationBatch) { b.Observations[0].Target = observation.GPUTarget("") }},
		{name: "two GPU identities", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].Target = observation.GPUTarget("GPU-1")
			b.Observations[0].Target.GetGpu().SourceLocalId = "slot"
		}},
		{name: "missing target kind", mutate: func(b *observation.ObservationBatch) { b.Observations[0].Target = &observation.ObservationTarget{} }},
		{name: "wrong unit", mutate: func(b *observation.ObservationBatch) { unit := "bytes"; b.Observations[0].Unit = &unit }},
		{name: "undefined no value", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].Outcome = &observation.Observation_NoValue{NoValue: &observation.NoValue{}}
		}},
		{name: "targeted no targets", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].Outcome = &observation.Observation_NoValue{NoValue: &observation.NoValue{Reason: observation.NoValueReasonNoTargets}}
		}},
		{name: "undefined error", mutate: func(b *observation.ObservationBatch) {
			b.Observations[0].Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{}}
		}},
		{name: "duplicate", mutate: func(b *observation.ObservationBatch) {
			b.Observations = append(b.Observations, proto.Clone(b.Observations[0]).(*observation.Observation))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newCollectRunner(t, time.Second)
			registerRunner(t, r, runnerSource{id: "bad", signals: collect.Signals{runnerCount}, measure: func(_ context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
				b := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID(), Observations: []*observation.Observation{runnerObservation(runnerCount, observation.NodeTarget(""), 4)}}
				tt.mutate(b)
				return b, nil
			}})
			r.Collect()
			obs := r.Observe(runnerCount.ID())
			if len(obs) != 1 || obs[0].GetCollectionError() == nil {
				t.Fatalf("malformed producer became successful: %v", obs)
			}
			if err := observation.ValidateBatch(&observation.ObservationBatch{CollectionId: "run-1", Observations: obs}); err != nil {
				t.Fatalf("invalid synthetic failure: %v", err)
			}
		})
	}
}

func TestCollectRunnerFailureAndValidPartialResults(t *testing.T) {
	tests := []struct {
		measure      func(context.Context, *collect.Op) (*observation.ObservationBatch, error)
		name         string
		wantObs      int
		failureFirst bool
	}{
		{name: "nil batch", measure: func(context.Context, *collect.Op) (*observation.ObservationBatch, error) { return nil, nil }, wantObs: 1},
		{name: "panic", measure: func(context.Context, *collect.Op) (*observation.ObservationBatch, error) { panic("broken") }, wantObs: 1},
		{name: "panic before healthy sensor", measure: func(context.Context, *collect.Op) (*observation.ObservationBatch, error) { panic("broken") }, wantObs: 1, failureFirst: true},
		{name: "top-level error", measure: func(context.Context, *collect.Op) (*observation.ObservationBatch, error) {
			return nil, errors.New("failed")
		}, wantObs: 1},
		{name: "valid partial and error", measure: func(_ context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
			b, _ := runnerBatch(runnerObservation(runnerCount, observation.NodeTarget(""), 4))(context.Background(), op)
			return b, errors.New("later operation failed")
		}, wantObs: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newCollectRunner(t, time.Second)
			healthy := collect.NewSignal("private.healthy_cpu_count", "1")
			sources := []runnerSource{
				{id: "healthy", signals: collect.Signals{healthy}, measure: runnerBatch(runnerObservation(healthy, observation.NodeTarget(""), 4))},
				{id: "source", signals: collect.Signals{runnerCount}, measure: tt.measure},
			}
			if tt.failureFirst {
				sources[0], sources[1] = sources[1], sources[0]
			}
			for _, source := range sources {
				registerRunner(t, r, source)
			}
			r.Collect()
			adapter := countAdapter(healthy.ID())
			adapter.Name = "healthy-cpu"
			adapter.Build = func(obs []*observation.Observation) []health.Measurement {
				var measurements []health.Measurement
				for _, o := range obs {
					if o.GetValue() != nil {
						measurements = append(measurements, cpu.CPUMeasurement{PossibleCPUCount: 4, OnlineCPUCount: o.GetValue().GetIntValue()})
					}
				}
				return measurements
			}
			adapter.Errored = func(err error) []health.Measurement {
				return []health.Measurement{cpu.ErroredCPU(err, cpu.MeasurementSignals()...)}
			}
			cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
			for _, check := range []health.CheckDefinition{cpu.CPUCount(), node.GPUCount()} {
				if err := cat.Register(check); err != nil {
					t.Fatal(err)
				}
			}
			sensors := []toolkit.Sensor{r.Sensor(adapter), r.Sensor(countAdapter(runnerCount.ID()))}
			if tt.failureFirst {
				sensors[0], sensors[1] = sensors[1], sensors[0]
			}
			verdict, err := toolkit.Collect(cat, sensors...)
			if err != nil {
				t.Fatal(err)
			}
			results := make(map[string]health.CheckVerdict)
			for _, result := range verdict.CheckResults {
				results[result.CheckID] = result
			}
			if result, ok := results["cpu_count"]; !ok || result.Severity != health.Pass || result.ObservedValues[cpu.SignalOnlineCPUCount] != int64(4) {
				t.Fatalf("healthy sensor did not reach evaluation: %+v", verdict)
			}
			if result, ok := results["gpu_count"]; !ok || result.Severity != health.Critical {
				t.Fatalf("failed sensor did not degrade independently: %+v", verdict)
			}
			obs := r.Observe(healthy.ID(), runnerCount.ID())
			healthyIndex, failureStart := 0, 1
			if tt.failureFirst {
				healthyIndex, failureStart = tt.wantObs, 0
			}
			if len(obs) != 1+tt.wantObs || obs[healthyIndex].GetValue().GetIntValue() != 4 || obs[failureStart+tt.wantObs-1].GetCollectionError() == nil {
				t.Fatalf("failure lost: %v", obs)
			}
			if tt.wantObs == 2 && obs[failureStart].GetValue().GetIntValue() != 4 {
				t.Fatalf("partial data lost: %v", obs)
			}
		})
	}
}

func TestCollectRunnerEmptySuccessReachesAdapter(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	registerRunner(t, r, runnerSource{id: "empty", signals: collect.Signals{runnerCount}, measure: runnerBatch()})
	r.Collect()
	seen := false
	a := countAdapter(runnerCount.ID())
	a.Build = func(obs []*observation.Observation) []health.Measurement { seen = len(obs) == 0; return nil }
	if _, err := r.Sensor(a).Measure(context.Background()); err != nil || !seen {
		t.Fatalf("empty success skipped adapter: seen=%v err=%v", seen, err)
	}
}

func TestCollectRunnerCacheOwnsClones(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	original := runnerObservation(runnerCount, observation.NodeTarget(""), 4)
	calls := 0
	registerRunner(t, r, runnerSource{id: "cache", signals: collect.Signals{runnerCount}, measure: func(ctx context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
		calls++
		return runnerBatch(original)(ctx, op)
	}})
	r.Collect()
	first := r.Observe(runnerCount.ID(), runnerCount.ID())
	if len(first) != 1 {
		t.Fatalf("repeated request duplicated: %v", first)
	}
	first[0].GetValue().Kind = &observation.Value_IntValue{IntValue: 99}
	original.GetValue().Kind = &observation.Value_IntValue{IntValue: 88}
	obs := r.Observe(runnerCount.ID())
	if obs[0].GetValue().GetIntValue() != 4 {
		t.Fatalf("cache mutated: %v", obs)
	}
	obs[0].Source = "mutated"
	if again := r.Observe(runnerCount.ID()); calls != 1 || again[0].GetSource() != "fake" {
		t.Fatalf("cache reuse mutated: %v calls=%d", again, calls)
	}
}

func TestCollectRunnerPreservesEarlierResultAfterLaterTimeout(t *testing.T) {
	r := newCollectRunner(t, 5*time.Millisecond)
	other := collect.NewSignal("private.other", "1")
	release := make(chan struct{})
	defer close(release)
	registerRunner(t, r, runnerSource{id: "good", signals: collect.Signals{runnerCount}, measure: runnerBatch(runnerObservation(runnerCount, observation.NodeTarget(""), 4))})
	// stuck ignores its context, so only the cycle timeout can end its wait.
	registerRunner(t, r, runnerSource{id: "stuck", signals: collect.Signals{other}, measure: func(context.Context, *collect.Op) (*observation.ObservationBatch, error) { <-release; return nil, nil }})
	r.Collect()
	obs := r.Observe(runnerCount.ID(), other.ID())
	if len(obs) != 2 || obs[0].GetValue().GetIntValue() != 4 || obs[1].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("earlier result lost: %v", obs)
	}
}

func TestCollectRunnerCanonicalTargetRules(t *testing.T) {
	for _, name := range []string{"wrong target", "NVLink endpoint", "GPU pair", "unverified value"} {
		t.Run(name, func(t *testing.T) {
			o := runnerObservation(gpusignals.PerformanceState, observation.GPUTarget("GPU-1"), 0)
			switch name {
			case "wrong target":
				o.Target = observation.NodeTarget("")
			case "NVLink endpoint":
				o.Target = observation.NVLinkEndpointTarget("GPU-1", 0)
			case "GPU pair":
				o.Target = observation.GPUPairTarget("GPU-1", "GPU-2")
			case "unverified value":
				o.Target = observation.GPUSourceLocalTarget("1")
			}
			r := newCollectRunner(t, time.Second)
			registerRunner(t, r, runnerSource{id: "gpu", signals: collect.Signals{gpusignals.PerformanceState}, measure: runnerBatch(o)})
			r.Collect()
			if got := r.Observe(gpusignals.PerformanceState.ID()); got[0].GetCollectionError() == nil {
				t.Fatalf("canonical violation became success: %v", got)
			}
		})
	}
}

// privateUnitSignal declares a non-canonical signal with an explicit unit pointer.
type privateUnitSignal struct {
	unit *string
	id   string
}

func (s privateUnitSignal) ID() string    { return s.id }
func (s privateUnitSignal) Unit() *string { return s.unit }

func TestCollectRunnerPreservesPrivateUnitPresence(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	unit := ""
	s := privateUnitSignal{id: "private.unit", unit: &unit}
	registerRunner(t, r, runnerSource{id: "unit", signals: collect.Signals{s}, measure: runnerBatch(runnerObservation(s, observation.NodeTarget(""), 1))})
	r.Collect()
	got := r.Observe(s.ID())
	if len(got) != 1 || got[0].GetCollectionError() != nil || got[0].Unit == nil || *got[0].Unit != "" {
		t.Fatalf("private unit presence lost: %v", got)
	}
}

func TestCollectRunnerPreservesDefinedAbsenceAndFailureOutcomes(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	zero, failed, unsupported, noTargets := collect.NewSignal("private.zero", "1"), collect.NewSignal("private.failed", "1"), collect.NewSignal("private.unsupported", "1"), collect.NewSignal("private.empty", "1")
	obs := []*observation.Observation{
		runnerObservation(zero, observation.NodeTarget(""), 0),
		observation.NewCollectionErrorObservation(failed.ID(), failed.Unit(), "fake", "read", observation.GPUTarget("GPU-1"), nil, &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: "unreadable"}),
		observation.NewNoValueObservation(unsupported.ID(), unsupported.Unit(), "fake", observation.GPUTarget("GPU-1"), nil, &observation.NoValue{Reason: observation.NoValueReasonNotSupported}),
		observation.NewNoValueObservation(noTargets.ID(), noTargets.Unit(), "fake", nil, nil, &observation.NoValue{Reason: observation.NoValueReasonNoTargets}),
	}
	registerRunner(t, r, runnerSource{id: "outcomes", signals: collect.Signals{zero, failed, unsupported, noTargets}, measure: runnerBatch(obs...)})
	r.Collect()
	seen := false
	a := countAdapter(zero.ID(), failed.ID(), unsupported.ID(), noTargets.ID())
	a.Build = func(got []*observation.Observation) []health.Measurement {
		seen = true
		if len(got) != 4 || got[0].GetValue() == nil || got[0].GetValue().GetIntValue() != 0 || got[1].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable || got[2].GetNoValue().GetReason() != observation.NoValueReasonNotSupported || got[3].GetNoValue().GetReason() != observation.NoValueReasonNoTargets || got[3].GetTarget() != nil {
			t.Errorf("defined outcomes changed: %v", got)
		}
		return nil
	}
	if _, err := r.Sensor(a).Measure(context.Background()); err != nil || !seen {
		t.Fatalf("adapter skipped: seen=%v err=%v", seen, err)
	}
}

func TestCollectRunnerDistinctSensorsShareCallerOwnedSnapshot(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	other := collect.NewSignal("private.other", "1")
	var acquired sync.Once
	var acquisitions int
	var captured *collect.Cycle
	snapshot := int64(0)
	readShared := func(cycle *collect.Cycle) int64 {
		acquired.Do(func() { acquisitions++; captured = cycle; snapshot = 4 })
		if cycle != captured {
			t.Error("shared source observed a different collection cycle")
		}
		return snapshot
	}
	for _, s := range []collect.Signal{runnerCount, other} {
		current := s
		registerRunner(t, r, runnerSource{id: s.ID(), signals: collect.Signals{s}, measure: func(ctx context.Context, op *collect.Op) (*observation.ObservationBatch, error) {
			return runnerBatch(runnerObservation(current, observation.NodeTarget(""), readShared(op.Cycle())))(ctx, op)
		}})
	}
	r.Collect()
	obs := r.Observe(runnerCount.ID(), other.ID())
	r.Observe(runnerCount.ID(), other.ID())
	if acquisitions != 1 || len(obs) != 2 || obs[0].GetValue().GetIntValue() != 4 || obs[1].GetValue().GetIntValue() != 4 {
		t.Fatalf("shared snapshot repeated: acquisitions=%d batch=%v", acquisitions, obs)
	}
}

func xidValue(s collect.Signal, cursor string, v *observation.Value) *observation.Observation {
	return &observation.Observation{SignalId: s.ID(), Source: "xid", Target: observation.JournalEntryTarget("boot", cursor), ObservedAt: timestamppb.Now(), Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v}}
}

func xidCode(cursor string, code int64) *observation.Observation {
	return xidValue(xidsignals.Code, cursor, &observation.Value{Kind: &observation.Value_IntValue{IntValue: code}})
}

func xidPCI(cursor string) *observation.Observation {
	return xidValue(xidsignals.PCIAddress, cursor, &observation.Value{Kind: &observation.Value_StringValue{StringValue: "0000:01:00"}})
}

func xidMarker() *observation.Observation {
	return &observation.Observation{SignalId: xidsignals.ScanWindow.ID(), Source: "xid", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(), Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: "boot"}}}}
}

func xidParseError(cursor string) *observation.Observation {
	return observation.NewCollectionErrorObservation(xidsignals.Code.ID(), xidsignals.Code.Unit(), "xid", "parse", observation.JournalEntryTarget("boot", cursor), nil,
		&observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, SourceError: "unrecognized_format"})
}

func TestCollectRunnerKeepsJournalEntryEventsDistinct(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	registerRunner(t, r, runnerSource{id: "xid", signals: xidsignals.All(), measure: runnerBatch(
		xidCode("c1", 79), xidCode("c2", 79), xidParseError("c3"), xidParseError("c4"), xidMarker())})
	r.Collect()
	got := r.Observe(xidsignals.Code.ID())
	if len(got) != 4 || got[0].GetValue().GetIntValue() != 79 || got[1].GetValue().GetIntValue() != 79 ||
		got[2].GetCollectionError().GetSourceError() != "unrecognized_format" || got[3].GetCollectionError().GetSourceError() != "unrecognized_format" {
		t.Fatalf("journal entries collapsed or failed: %v", got)
	}
}

func TestCollectRunnerPreservesSensorBatchOrder(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	batch := []*observation.Observation{xidCode("c1", 79), xidPCI("c1"), xidCode("c2", 48), xidPCI("c2"), xidPCI("c3"), xidCode("c3", 31), xidMarker()}
	registerRunner(t, r, runnerSource{id: "xid", signals: xidsignals.All(), measure: runnerBatch(batch...)})
	r.Collect()
	got := r.Observe(xidsignals.All().IDs()...)
	if len(got) != len(batch) {
		t.Fatalf("got %d observations, want %d: %v", len(got), len(batch), got)
	}
	for i, o := range got {
		if !proto.Equal(o, batch[i]) {
			t.Fatalf("observation %d = %v, want %v", i, o, batch[i])
		}
	}
}

func TestCollectRunnerRejectsFutureJournalEntry(t *testing.T) {
	r := newCollectRunner(t, time.Second)
	future := xidCode("c1", 79)
	future.ObservedAt = timestamppb.New(time.Now().Add(time.Hour))
	registerRunner(t, r, runnerSource{id: "xid", signals: xidsignals.All(), measure: runnerBatch(future, xidMarker())})
	r.Collect()
	got := r.Observe(xidsignals.All().IDs()...)
	if len(got) != len(xidsignals.All()) {
		t.Fatalf("got %d observations, want one failure per owned signal: %v", len(got), got)
	}
	for _, o := range got {
		if o.GetTarget() != nil || o.GetCollectionError().GetCategory() != observation.CollectionErrorCategorySourceError {
			t.Fatalf("future entry not rejected: %v", o)
		}
	}
}
