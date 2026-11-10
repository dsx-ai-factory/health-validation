// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// TestGPUCollectParity drives the legacy NVML GPU probe through go-nvml mocks
// and the collect path through DCGM-shaped observations for the same GPU
// states, then compares the four DCGM-backed checks' fields, failed signals,
// and verdicts. The two paths read different libraries, so they share no
// fixture; collect's own tests cover DCGM decoding. Remove with the legacy
// GPU sensor.
//
// Expected differences, listed in advance:
//   - NVML reports milliwatts and DCGM watts; fixture values are exact in both.
//   - Failure messages name different libraries and operations, so only
//     failed signals and verdicts are compared, not error text.
//   - gpu_compute_apps is compared in TestGPUComputeProcessParity, which
//     drives collect's NVML process sensor alongside the DCGM fake.
//   - The unsupported throttle case feeds NoValue(NOT_SUPPORTED), which is
//     what collect's DCGM source decodes the clock-event NOT_SUPPORTED
//     sentinel to.
//   - A GPU whose UUID cannot be read is named differently on each path
//     (legacy by its domain, collect by its source-local slot), so only
//     verdicts are compared for that case.
func TestGPUCollectParity(t *testing.T) {
	tests := []struct {
		name string
		// gpus are the per-GPU states; nil enumerates no GPUs.
		gpus []gpuState
		// enumerationFails fails DeviceGetCount and DCGM GPU identity reads.
		enumerationFails bool
	}{
		{name: "healthy", gpus: []gpuState{{}}},
		{name: "thermal slowdown", gpus: []gpuState{{thermal: true}}},
		{name: "hardware slowdown and power brake", gpus: []gpuState{{slowdown: true, powerBrake: true}}},
		{name: "P8", gpus: []gpuState{{pstate: 8}}},
		{name: "unknown pstate", gpus: []gpuState{{pstate: int64(gonvml.PSTATE_UNKNOWN)}}},
		{name: "power limit mismatch", gpus: []gpuState{{limitMW: 1_000_000}}},
		{name: "power above range", gpus: []gpuState{{powerMW: 1_600_000}}},
		{name: "throttle unsupported", gpus: []gpuState{{throttleUnsupported: true}}},
		{name: "pstate read fails", gpus: []gpuState{{pstateFails: true}}},
		{name: "power read fails", gpus: []gpuState{{powerFails: true}}},
		{name: "two GPUs, one throttling", gpus: []gpuState{{}, {thermal: true}}},
		{name: "UUID unreadable", gpus: []gpuState{{}, {uuidFails: true}}},
		{name: "enumeration fails", enumerationFails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := measure(t, toolkit.NewGPUSensor(nvml.NewWithLibrary(nvmlLibrary(tt.gpus, tt.enumerationFails, nil))))
			bridged := measure(t, bridgedGPUSensor(t, collectCycle(t, time.Minute), dcgmFake{obs: dcgmObservations(tt.gpus, tt.enumerationFails)}, nil))

			if !tt.enumerationFails && !slices.ContainsFunc(tt.gpus, func(s gpuState) bool { return s.uuidFails }) {
				if l, c := gpuFacts(t, legacy), gpuFacts(t, bridged); !reflect.DeepEqual(l, c) {
					t.Errorf("measurements differ:\n legacy=%+v\n collect=%+v", l, c)
				}
			}
			if l, c := parityVerdicts(t, evaluateGPU(t, legacy...)), parityVerdicts(t, evaluateGPU(t, bridged...)); !reflect.DeepEqual(l, c) {
				t.Errorf("verdicts differ:\n legacy=%v\n collect=%v", l, c)
			}
		})
	}
}

// TestGPUCollectTimeoutParity stalls both paths past their timeout. Both must
// fail every check with a retry-collection action.
func TestGPUCollectTimeoutParity(t *testing.T) {
	const timeout = 10 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	legacy, err := toolkit.CollectWithOptions(gpuCatalog(t), toolkit.CollectionOptions{Timeout: timeout},
		toolkit.NewGPUSensor(nvml.NewWithLibrary(nvmlLibrary([]gpuState{{}}, false, release))))
	if err != nil {
		t.Fatal(err)
	}
	bridged, err := toolkit.CollectWithOptions(gpuCatalog(t), toolkit.CollectionOptions{Timeout: time.Minute},
		bridgedGPUSensor(t, collectCycle(t, timeout), dcgmFake{obs: dcgmObservations([]gpuState{{}}, false), block: release}, nil))
	if err != nil {
		t.Fatal(err)
	}

	l, c := parityVerdicts(t, legacy), parityVerdicts(t, bridged)
	if !reflect.DeepEqual(l, c) {
		t.Errorf("verdicts differ:\n legacy=%v\n collect=%v", l, c)
	}
	for _, v := range c {
		if v.severity != health.Unknown || !v.action.Equal(health.Drain(health.RemediationActionRetryCollection)) {
			t.Errorf("%+v, want UNKNOWN with retry collection", v)
		}
	}
}

// gpuState describes one GPU; the zero value is healthy on GB200.
type gpuState struct {
	pids                []uint32 // compute processes NVML reports
	pstate              int64
	powerMW             uint32 // zero means 1100 W
	limitMW             uint32 // requested limit; zero means 1200 W
	thermal             bool
	slowdown            bool
	powerBrake          bool
	throttleUnsupported bool
	pstateFails         bool
	powerFails          bool
	uuidFails           bool
	processesFail       bool
}

func (s gpuState) power() uint32 { return cmp.Or(s.powerMW, 1_100_000) }
func (s gpuState) limit() uint32 { return cmp.Or(s.limitMW, 1_200_000) }

func gpuUUID(i int) string { return fmt.Sprintf("GPU-%d", i) }

// nvmlLibrary mocks NVML for gpus. A non-nil block stalls enumeration until closed.
func nvmlLibrary(gpus []gpuState, enumerationFails bool, block <-chan struct{}) gonvml.Interface {
	devices := make([]gonvml.Device, len(gpus))
	for i, s := range gpus {
		devices[i] = nvmlDevice(gpuUUID(i), s)
	}
	return &mock.Interface{
		InitFunc:     func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			if block != nil {
				<-block
			}
			if enumerationFails {
				return 0, gonvml.ERROR_UNKNOWN
			}
			return len(devices), gonvml.SUCCESS
		},
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) { return devices[i], gonvml.SUCCESS },
	}
}

func nvmlDevice(uuid string, s gpuState) *mock.Device {
	return &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) {
			if s.uuidFails {
				return "", gonvml.ERROR_UNKNOWN
			}
			return uuid, gonvml.SUCCESS
		},
		GetPerformanceStateFunc: func() (gonvml.Pstates, gonvml.Return) {
			if s.pstateFails {
				return 0, gonvml.ERROR_UNKNOWN
			}
			return gonvml.Pstates(s.pstate), gonvml.SUCCESS
		},
		GetFieldValuesFunc: func(values []gonvml.FieldValue) gonvml.Return {
			if s.powerFails {
				return gonvml.ERROR_UNKNOWN
			}
			values[0].NvmlReturn = uint32(gonvml.SUCCESS)
			values[0].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
			binary.LittleEndian.PutUint32(values[0].Value[:4], s.power())
			return gonvml.SUCCESS
		},
		GetPowerManagementLimitFunc: func() (uint32, gonvml.Return) { return s.limit(), gonvml.SUCCESS },
		GetEnforcedPowerLimitFunc:   func() (uint32, gonvml.Return) { return 1_200_000, gonvml.SUCCESS },
		GetCurrentClocksEventReasonsFunc: func() (uint64, gonvml.Return) {
			if s.throttleUnsupported {
				return 0, gonvml.ERROR_NOT_SUPPORTED
			}
			var mask uint64
			if s.slowdown {
				mask |= gonvml.ClocksThrottleReasonHwSlowdown
			}
			if s.thermal {
				mask |= gonvml.ClocksThrottleReasonHwThermalSlowdown
			}
			if s.powerBrake {
				mask |= gonvml.ClocksThrottleReasonHwPowerBrakeSlowdown
			}
			return mask, gonvml.SUCCESS
		},
		GetComputeRunningProcessesFunc: func() ([]gonvml.ProcessInfo, gonvml.Return) { return processInfos(s) },
	}
}

// dcgmSignals are the signals collect's DCGM sensor supplies to the GPU adapter.
var dcgmSignals = collect.Signals{
	gpusignals.PowerDraw, gpusignals.PowerLimitRequested, gpusignals.PowerLimitEnforced,
	gpusignals.PerformanceState, gpusignals.HardwareSlowdownActive,
	gpusignals.HardwareThermalSlowdownActive, gpusignals.HardwarePowerBrakeSlowdownActive,
}

// dcgmObservations is what collect's DCGM sensor emits for gpus.
func dcgmObservations(gpus []gpuState, enumerationFails bool) []*observation.Observation {
	now := timestamppb.Now()
	failed := func(signal collect.Signal, target *observation.ObservationTarget) *observation.Observation {
		return observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), "dcgm", "metrics", target, now,
			&observation.CollectionError{Category: observation.CollectionErrorCategorySourceError, Detail: "DCGM error"})
	}
	var out []*observation.Observation
	if enumerationFails {
		for _, signal := range dcgmSignals {
			out = append(out, failed(signal, nil))
		}
		return out
	}
	for i, s := range gpus {
		target := observation.GPUTarget(gpuUUID(i))
		if s.uuidFails {
			target = observation.GPUSourceLocalTarget(fmt.Sprintf("dcgm-index-%d", i))
		}
		value := func(signal collect.Signal, v *observation.Value) *observation.Observation {
			return &observation.Observation{
				SignalId: signal.ID(), Source: "dcgm", Target: target, ObservedAt: now, Unit: signal.Unit(),
				Outcome: &observation.Observation_Value{Value: v},
			}
		}
		throttle := func(signal collect.Signal, active bool) *observation.Observation {
			if s.throttleUnsupported {
				return observation.NewNoValueObservation(signal.ID(), signal.Unit(), "dcgm", target, now,
					&observation.NoValue{Reason: observation.NoValueReasonNotSupported})
			}
			return value(signal, boolValue(active))
		}
		pstate := value(gpusignals.PerformanceState, intValue(s.pstate))
		if s.pstateFails {
			pstate = failed(gpusignals.PerformanceState, target)
		}
		power := value(gpusignals.PowerDraw, doubleValue(float64(s.power())/1000))
		if s.powerFails {
			power = failed(gpusignals.PowerDraw, target)
		}
		out = append(out,
			power,
			value(gpusignals.PowerLimitRequested, doubleValue(float64(s.limit())/1000)),
			value(gpusignals.PowerLimitEnforced, doubleValue(1200)),
			pstate,
			throttle(gpusignals.HardwareSlowdownActive, s.slowdown),
			throttle(gpusignals.HardwareThermalSlowdownActive, s.thermal),
			throttle(gpusignals.HardwarePowerBrakeSlowdownActive, s.powerBrake),
		)
	}
	return out
}

func boolValue(v bool) *observation.Value {
	return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}}
}

func intValue(v int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}
}

func doubleValue(v float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: v}}
}

// dcgmFake stands in for collect's DCGM sensor, whose source cannot be faked
// from outside collect. A non-nil block stalls Measure until closed.
type dcgmFake struct {
	block <-chan struct{}
	obs   []*observation.Observation
}

func (dcgmFake) ID() string               { return "gpu.dcgm" }
func (dcgmFake) Signals() collect.Signals { return dcgmSignals }

func (f dcgmFake) Measure(ctx context.Context, options ...collect.OpOption) (*observation.ObservationBatch, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &observation.ObservationBatch{CollectionId: collect.NewOp(options...).Cycle().CollectionID(), Observations: f.obs}, nil
}

func collectCycle(t *testing.T, timeout time.Duration) *collect.Cycle {
	t.Helper()
	cycle, err := collect.NewCycleWithTimeout("parity", time.Now(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	return cycle
}

// bridgedGPUSensor runs dcgm, and processes when given, through the bridge and
// the built-in GPU adapter, as the stock CLI registers them. Without processes
// the compute-process signals are not requested.
func bridgedGPUSensor(t *testing.T, cycle *collect.Cycle, dcgm, processes collect.Sensor) toolkit.Sensor {
	t.Helper()
	adapter := collectadapters.GPU()
	isDCGM := func(id string) bool { return slices.Contains(dcgmSignals.IDs(), id) }
	signals := slices.Sorted(maps.Keys(adapter.Signals()))
	// This parity fixture supplies DCGM fields and, optionally, NVML processes.
	// PCI properties have their own source and are outside this comparison.
	signals = slices.DeleteFunc(signals, func(id string) bool {
		return !isDCGM(id) && (processes == nil || !slices.Contains(processes.Signals().IDs(), id))
	})
	runner := collectbridge.NewRunner(cycle)
	if err := runner.Register(dcgm, slices.DeleteFunc(slices.Clone(signals), func(id string) bool { return !isDCGM(id) })...); err != nil {
		t.Fatal(err)
	}
	if processes == nil {
		signals = slices.DeleteFunc(signals, func(id string) bool { return !isDCGM(id) })
	} else if err := runner.Register(processes, slices.DeleteFunc(slices.Clone(signals), isDCGM)...); err != nil {
		t.Fatal(err)
	}
	runner.Collect()
	return runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: signals, Category: adapter.Category(), Build: adapter.Build, Errored: adapter.Errored,
	})
}

func measure(t *testing.T, sensor toolkit.Sensor) []health.Measurement {
	t.Helper()
	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		measurements = sensor.Errored(err)
	}
	return measurements
}

// gpuFact is the part of a GPU measurement the four DCGM-backed checks read.
type gpuFact struct {
	id                            string
	failed                        []string
	power, limit, enforced        float64
	pstate                        int64
	slowdown, thermal, powerBrake bool
}

// computeSignals are compared separately, by TestGPUComputeProcessParity.
var computeSignals = []string{gpu.SignalComputeProcessCount, gpu.SignalComputeProcessCmdlines}

// gpuFacts compares DCGM-backed fields, excluding process and PCI signals supplied by other sources.
func gpuFacts(t *testing.T, measurements []health.Measurement) []gpuFact {
	t.Helper()
	var out []gpuFact
	for _, m := range measurements {
		g, ok := m.(gpu.GPUMeasurement)
		if !ok {
			t.Fatalf("got %T, want gpu.GPUMeasurement", m)
		}
		var failed []string
		for _, e := range g.Errors() {
			failed = append(failed, e.FailedSignals()...)
		}
		failed = slices.DeleteFunc(failed, func(s string) bool {
			return slices.Contains(computeSignals, s) || s == gpu.SignalPCIeMaxLinkWidth || s == gpu.SignalPCIeMaxLinkGeneration
		})
		slices.Sort(failed)
		if failed = slices.Compact(failed); len(failed) == 0 {
			failed = nil
		}
		out = append(out, gpuFact{
			id: g.GPUID, failed: failed,
			power: g.AveragePowerDrawW, limit: g.PowerLimitW, enforced: g.EnforcedPowerLimitW, pstate: g.GPUPstate,
			slowdown: g.HWSlowdown, thermal: g.HWThermalSlowdown, powerBrake: g.HWPowerBrakeSlowdown,
		})
	}
	slices.SortFunc(out, func(a, b gpuFact) int { return cmp.Compare(a.id, b.id) })
	return out
}

func gpuCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{
		gpu.GPUAveragePowerDraw(), gpu.GPUPowerLimit(), gpu.GPUPowerState(), gpu.GPUSensorThrottle(),
	} {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

func evaluateGPU(t *testing.T, measurements ...health.Measurement) health.NodeVerdict {
	t.Helper()
	verdict, err := gpuCatalog(t).EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

type parityVerdict struct {
	check    string
	action   health.RecommendedAction
	severity health.Severity
}

// parityVerdicts lists each check result, ignoring entity names, which differ
// for whole-domain failures.
func parityVerdicts(t *testing.T, verdict health.NodeVerdict) []parityVerdict {
	t.Helper()
	var out []parityVerdict
	for _, result := range verdict.CheckResults {
		out = append(out, parityVerdict{check: result.CheckID, action: result.RecommendedAction, severity: result.Severity})
	}
	if len(out) == 0 {
		t.Fatal("no checks evaluated")
	}
	slices.SortFunc(out, func(a, b parityVerdict) int {
		return cmp.Or(cmp.Compare(a.check, b.check), cmp.Compare(fmt.Sprint(a.severity, a.action), fmt.Sprint(b.severity, b.action)))
	})
	return out
}
