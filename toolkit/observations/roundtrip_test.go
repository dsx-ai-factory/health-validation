// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// Evaluating a serialized batch must give the verdict evaluating the
// measurements directly gives: that is what an evaluate mode relies on.
func TestRoundTripPreservesMeasurementsAndVerdicts(t *testing.T) {
	original := roundTripFixture()

	var exported []*observation.Observation
	for _, m := range original {
		observations, err := FromMeasurements(m.Category().String(), testNode, time.Unix(1, 0), m)
		if err != nil {
			t.Fatal(err)
		}
		exported = append(exported, observations...)
	}
	wire, err := protojson.Marshal(&observation.ObservationBatch{CollectionId: "c", Observations: exported})
	if err != nil {
		t.Fatal(err)
	}
	var batch observation.ObservationBatch
	if err := protojson.Unmarshal(wire, &batch); err != nil {
		t.Fatal(err)
	}
	restored := ToMeasurements(batch.GetObservations())

	if got, want := byEntity(t, restored), byEntity(t, original); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored measurements differ:\n got  %v\n want %v", got, want)
	}
	for _, scope := range []struct {
		arch      string
		execution health.ExecutionContext
	}{
		{"GB200", health.OperatorInfra},
		{"H100", health.TenantGuest},
	} {
		got, want := verdictSummary(t, scope.arch, scope.execution, restored), verdictSummary(t, scope.arch, scope.execution, original)
		if !slices.Equal(got, want) {
			t.Fatalf("%s/%s verdicts differ:\n got  %v\n want %v", scope.arch, scope.execution, got, want)
		}
	}
}

func TestToMeasurementsMergesToolkitAndMappedSignals(t *testing.T) {
	observations, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 2})
	if err != nil {
		t.Fatal(err)
	}
	// A GPU check migrated to a collect sensor: its signal arrives under the
	// mapped ID instead of the toolkit field.
	observations = slices.DeleteFunc(observations, func(o *observation.Observation) bool {
		return o.GetSignalId() == "gpu.power_limit_w"
	})
	observations = append(observations, valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700)))

	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if m.PowerLimitW != 700 || m.GPUPstate != 2 || len(m.Errors()) != 0 {
		t.Fatalf("unexpected measurement %+v, errors %v", m, m.Errors())
	}
}

func TestToMeasurementsFailsSignalFedTwice(t *testing.T) {
	observations, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil {
		t.Fatal(err)
	}
	observations = append(observations, valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700)))

	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if got := failedSignals(m); !slices.Equal(got, []string{gpu.SignalPowerLimitW}) {
		t.Fatalf("failed signals = %v", got)
	}
}

func TestToMeasurementsFailsValueThatDoesNotFitField(t *testing.T) {
	observations, err := FromMeasurements("xid", testNode, time.Unix(1, 0), xid.XidMeasurement{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range observations {
		switch o.GetSignalId() {
		case "xid.event_count":
			o.Outcome = &observation.Observation_Value{Value: boolValue(true)}
		case "xid.events":
			o.Outcome = &observation.Observation_Value{Value: stringValue("not json")}
		}
	}

	m := only[xid.XidMeasurement](t, ToMeasurements(observations))
	got := failedSignals(m)
	slices.Sort(got)
	if !slices.Equal(got, []string{"event_count", "events"}) {
		t.Fatalf("failed signals = %v", got)
	}
}

func TestToMeasurementsIgnoresUnknownSignals(t *testing.T) {
	unknown := []*observation.Observation{
		valueObservation("gpu.not_a_field", intValue(1)),
		valueObservation("storage.capacity", intValue(1)),
	}
	if measurements := ToMeasurements(unknown); len(measurements) != 0 {
		t.Fatalf("want no measurements, got %v", measurements)
	}
}

func roundTripFixture() []health.Measurement {
	partialGPU := gpu.GPUMeasurement{GPUID: "GPU-1", PowerLimitW: 700, EnforcedPowerLimitW: 690, HWThermalSlowdown: true}
	partialGPU.AddError(fmt.Errorf("pstate read: %w", health.ErrCollectionTimeout), gpu.SignalGPUPstate)
	partialGPU.MarkUnsupported(gpu.SignalHWPowerBrakeSlowdown)

	link := nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18, InactiveCount: 1, AllLinksSupportP2P: true}
	link.AddError(errors.New("nvlink speed read failed"), nvlink.SignalSpeedMBytesPerSec)
	link.MarkUnsupported(nvlink.SignalFabricRouteUnhealthy)

	return []health.Measurement{
		gpu.GPUMeasurement{
			GPUID: "GPU-0", AveragePowerDrawW: 412.5, PowerLimitW: 700, EnforcedPowerLimitW: 700,
			ComputeProcessCount: 1, ComputeProcessCmdlines: "123 'python train.py'",
		},
		partialGPU,
		gpu.ErroredGPU("gpu", errors.New("nvml device handle failed"), gpu.MeasurementSignals()...),
		link,
		cpu.CPUMeasurement{PossibleCPUCount: 64, OnlineCPUCount: 63, NUMANodeCount: 2},
		node.NodeMeasurement{GPUCount: 8},
		xid.XidMeasurement{
			Since:          "boot",
			EventCount:     2,
			XidCounts:      map[int64]int64{79: 1, 149: 1},
			FatalXidCounts: map[int64]int64{79: 1},
			Events: []xid.XidEvent{
				{XidCode: 79, PCIAddress: "0000:01:00", ProcessName: "python", Pid: 7},
				{XidCode: 149, PCIAddress: "0000:02:00", NVLink5: &xid.NVLink5Detail{SubComponent: "NETIR", Link: 3, Fatal: true, DebugData: []string{"0x1"}}},
			},
		},
		infiniband.LinkFlapMeasurement{
			Since:           "boot",
			OutageDurations: []int64{45},
			FlapEvents:      []infiniband.LinkFlapEvent{{Interface: "mlx5_0", DownAt: "t0", UpAt: "t1", DurationSeconds: 45}},
		},
	}
}

// byEntity summarizes measurements by type and GPU so the comparison ignores
// order: every json field, each error's message, timeout, and signals, and
// the unsupported signals. An empty map or slice compares equal to nil.
func byEntity(t *testing.T, measurements []health.Measurement) map[string]string {
	t.Helper()
	out := make(map[string]string, len(measurements))
	for _, m := range measurements {
		v := reflect.ValueOf(m)
		fields := make(map[string]any)
		var id string
		for i := range v.NumField() {
			name, ok := signalName(v.Type().Field(i))
			if !ok {
				continue
			}
			value := v.Field(i)
			if (value.Kind() == reflect.Map || value.Kind() == reflect.Slice) && value.Len() == 0 {
				fields[name] = nil
				continue
			}
			fields[name] = value.Interface()
			if name == gpuIDField {
				id = value.String()
			}
		}
		var errs []string
		for _, entry := range m.Errors() {
			signals := slices.Sorted(slices.Values(entry.FailedSignals()))
			errs = append(errs, fmt.Sprintf("%s|timeout=%t|%v", entry.Err(), errors.Is(entry.Err(), health.ErrCollectionTimeout), signals))
		}
		slices.Sort(errs)
		var unsupported []string
		if reporter, ok := m.(health.UnsupportedSignaler); ok {
			unsupported = slices.Sorted(slices.Values(reporter.UnsupportedSignals()))
		}
		encoded, err := json.Marshal(struct {
			Fields      map[string]any
			Errors      []string
			Unsupported []string
		}{fields, errs, unsupported})
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("%T/%s", m, id)
		if _, dup := out[key]; dup {
			t.Fatalf("two measurements for %s", key)
		}
		out[key] = string(encoded)
	}
	return out
}

// verdictSummary renders each evaluated or skipped check's outcome, sorted,
// leaving out run-specific IDs and timestamps.
func verdictSummary(t *testing.T, arch string, execution health.ExecutionContext, measurements []health.Measurement) []string {
	t.Helper()
	verdict, err := catalog.New(catalog.WithArchitecture(arch), catalog.WithExecutionContext(execution)).EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, results := range [][]health.CheckVerdict{verdict.CheckResults, verdict.SkippedChecks} {
		for _, r := range results {
			encoded, err := json.Marshal(struct {
				Observed map[string]any
				ID       string
				Messages []string
				Entities []string
				Action   health.RecommendedAction
				Severity health.Severity
				Skip     health.SkipReason
			}{
				Observed: r.ObservedValues,
				ID:       r.CheckID,
				Messages: r.Messages,
				Entities: r.AffectedEntities,
				Action:   r.RecommendedAction,
				Severity: r.Severity,
				Skip:     r.SkipReason,
			})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(encoded))
		}
	}
	slices.Sort(out)
	if len(out) == 0 || !strings.Contains(strings.Join(out, ""), "CRITICAL") {
		t.Fatalf("fixture should drive at least one CRITICAL verdict: %v", out)
	}
	return out
}

// Only the fields the toolkit's sensors treat as neutral may be reported
// unsupported without failing; any other must not pass a check on its zero
// value, whoever produced the batch.
func TestToMeasurementsLimitsNeutralUnsupportedToToolkitSignals(t *testing.T) {
	observations, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range observations {
		switch o.GetSignalId() {
		case "gpu.gpu_pstate", "gpu.hw_slowdown":
			o.Outcome = &observation.Observation_NoValue{NoValue: &observation.NoValue{Reason: observation.NoValueReasonNotSupported}}
		}
	}

	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if got := failedSignals(m); !slices.Equal(got, []string{gpu.SignalGPUPstate}) {
		t.Fatalf("failed signals = %v, want only %s", got, gpu.SignalGPUPstate)
	}
	if got := m.UnsupportedSignals(); !slices.Equal(got, []string{gpu.SignalHWSlowdown}) {
		t.Fatalf("unsupported signals = %v, want only %s", got, gpu.SignalHWSlowdown)
	}
	if got := evaluate(t, "H100", []health.Measurement{m}, gpu.GPUPowerState())["gpu_power_state"]; got != health.Critical {
		t.Fatalf("gpu_power_state = %v, want CRITICAL", got)
	}

	want := []string{
		"gpu.hw_power_brake_slowdown", "gpu.hw_slowdown", "gpu.hw_thermal_slowdown",
		"nvlink.fabric_access_timeout_recovery", "nvlink.fabric_bandwidth_degraded",
		"nvlink.fabric_route_recovery_in_progress", "nvlink.fabric_route_unhealthy",
	}
	if got := slices.Sorted(maps.Keys(neutralFields())); !slices.Equal(got, want) {
		t.Fatalf("neutral fields = %v, want %v", got, want)
	}
}

// A signal named by several error entries keeps every message, and the
// round trip keeps the verdict's message and recommended action.
func TestRoundTripKeepsEveryErrorOnASignal(t *testing.T) {
	for name, errs := range map[string][]error{
		"timeout and failure": {
			fmt.Errorf("first read: %w", health.ErrCollectionTimeout),
			errors.New("second read failed"),
		},
		"two timeouts": {
			fmt.Errorf("first read: %w", health.ErrCollectionTimeout),
			fmt.Errorf("second read: %w", health.ErrCollectionTimeout),
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := gpu.GPUMeasurement{GPUID: "GPU-0", PowerLimitW: 700, EnforcedPowerLimitW: 700}
			for _, err := range errs {
				m.AddError(err, gpu.SignalGPUPstate)
			}

			observations, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), m)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range observations {
				if o.GetSignalId() != "gpu.gpu_pstate" {
					continue
				}
				failure := o.GetCollectionError()
				if want := errs[0].Error() + "; " + errs[1].Error(); failure.GetDetail() != want {
					t.Fatalf("detail = %q, want %q", failure.GetDetail(), want)
				}
				wantTimeout := errors.Is(errs[1], health.ErrCollectionTimeout)
				if got := failure.GetCategory() == observation.CollectionErrorCategoryTimeout; got != wantTimeout {
					t.Fatalf("category = %v, want timeout=%t", failure.GetCategory(), wantTimeout)
				}
			}

			restored := ToMeasurements(observations)
			original := []health.Measurement{m}
			if got, want := verdictSummary(t, "H100", health.OperatorInfra, restored), verdictSummary(t, "H100", health.OperatorInfra, original); !slices.Equal(got, want) {
				t.Fatalf("verdicts differ:\n got  %v\n want %v", got, want)
			}
		})
	}
}
