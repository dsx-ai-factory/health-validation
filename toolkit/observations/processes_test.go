// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

const testBootID = "01234567-89ab-cdef-0123-456789abcdef"

// processObservations is healthy GPU-0 telemetry plus a process count and the
// given gpu.process.cmdline observations.
func processObservations(count uint64, cmdlines ...*observation.Observation) []*observation.Observation {
	countObs := valueObservation(gpusignals.ProcessCount.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: count}})
	return append(append(healthyGPUObservations(), countObs), cmdlines...)
}

func cmdlineObservation(pid uint32, text string) *observation.Observation {
	o := valueObservation(gpusignals.ProcessCmdline.ID(), &observation.Value{Kind: &observation.Value_StringValue{StringValue: text}})
	o.Source = "nvml"
	o.Target = observation.GPUProcessTarget("GPU-0", testBootID, pid, 100)
	return o
}

func cmdlineFailure(pid uint32, operation string) *observation.Observation {
	o := failedObservation(gpusignals.ProcessCmdline.ID(), &observation.CollectionError{
		Category: observation.CollectionErrorCategoryUnavailable, Operation: operation, Detail: "cannot read",
	})
	o.Source = "nvml"
	if pid != 0 {
		o.Target = observation.GPUProcessTarget("GPU-0", testBootID, pid, 100)
	}
	return o
}

// computeAppsVerdict evaluates gpu_compute_apps on m.
func computeAppsVerdict(t *testing.T, m gpu.GPUMeasurement) health.CheckVerdict {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	if err := cat.Register(gpu.GPUComputeApps()); err != nil {
		t.Fatal(err)
	}
	verdict, err := cat.EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	return verdict.CheckResults[0]
}

func TestToMeasurementsComputeProcesses(t *testing.T) {
	long := strings.Repeat("a", procCmdlineMax+10)
	tests := []struct {
		name         string
		wantCmdlines string
		observations []*observation.Observation
		wantCount    int64
		wantSeverity health.Severity
	}{
		{
			name:         "zero processes",
			observations: processObservations(0, noValueObservation(gpusignals.ProcessCmdline.ID(), observation.NoValueReasonNoTargets)),
			wantSeverity: health.Pass,
		},
		{
			name:         "active processes",
			observations: processObservations(2, cmdlineObservation(10, "python train.py"), cmdlineObservation(11, "a\nb "+long)),
			wantCount:    2,
			wantCmdlines: `pid=10 "python train.py"` + "\n" + "pid=11 " + strconv.Quote(("a\nb " + long)[:procCmdlineMax]),
			wantSeverity: health.Critical,
		},
		{
			name:         "unreadable or empty cmdline keeps the pid",
			observations: processObservations(2, cmdlineFailure(12, "ReadCmdline"), cmdlineObservation(13, "")),
			wantCount:    2,
			wantCmdlines: "pid=12\npid=13",
			wantSeverity: health.Critical,
		},
		{
			name:         "unidentified process drops only its text",
			observations: processObservations(2, cmdlineObservation(14, "worker"), cmdlineFailure(0, opProcessIdentity)),
			wantCount:    2,
			wantCmdlines: `pid=14 "worker"`,
			wantSeverity: health.Critical,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := only[gpu.GPUMeasurement](t, ToMeasurements(test.observations))
			if failed := failedSignals(m); len(failed) != 0 {
				t.Fatalf("failed signals = %v", failed)
			}
			if m.ComputeProcessCount != test.wantCount || m.ComputeProcessCmdlines != test.wantCmdlines {
				t.Fatalf("count=%d cmdlines=%q, want %d %q", m.ComputeProcessCount, m.ComputeProcessCmdlines, test.wantCount, test.wantCmdlines)
			}
			if got := computeAppsVerdict(t, m); got.Severity != test.wantSeverity {
				t.Fatalf("gpu_compute_apps = %v, want %v", got.Severity, test.wantSeverity)
			}
		})
	}
}

// NVML failing to list processes fails the count and the cmdlines, as the
// legacy backend does, and a missing cmdline feed is not read as no processes.
func TestToMeasurementsComputeProcessFailures(t *testing.T) {
	listFailure := &observation.CollectionError{Category: observation.CollectionErrorCategorySourceError, Operation: "GetComputeRunningProcesses"}
	tests := []struct {
		name         string
		observations []*observation.Observation
		want         []string
	}{
		{
			name: "process list fails",
			observations: append(healthyGPUObservations(),
				failedObservation(gpusignals.ProcessCount.ID(), listFailure),
				failedObservation(gpusignals.ProcessCmdline.ID(), listFailure)),
			want: []string{gpu.SignalComputeProcessCount, gpu.SignalComputeProcessCmdlines},
		},
		{
			name:         "duplicate pid",
			observations: processObservations(1, cmdlineObservation(10, "a"), cmdlineObservation(10, "b")),
			want:         []string{gpu.SignalComputeProcessCmdlines},
		},
		{
			name:         "no processes and a process",
			observations: processObservations(1, noValueObservation(gpusignals.ProcessCmdline.ID(), observation.NoValueReasonNoTargets), cmdlineObservation(10, "a")),
			want:         []string{gpu.SignalComputeProcessCmdlines},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := only[gpu.GPUMeasurement](t, ToMeasurements(test.observations))
			failed := failedSignals(m)
			slices.Sort(failed)
			want := slices.Sorted(slices.Values(test.want))
			if !slices.Equal(failed, want) {
				t.Fatalf("failed signals = %v, want %v", failed, want)
			}
			if got := computeAppsVerdict(t, m); got.Severity != health.Unknown {
				t.Fatalf("gpu_compute_apps = %v, want UNKNOWN", got.Severity)
			}
		})
	}

	m := only[gpu.GPUMeasurement](t, ToMeasurements(processObservations(0)))
	if !slices.Contains(missingSignals(m), gpu.SignalComputeProcessCmdlines) {
		t.Fatalf("missing cmdlines not reported missing: %v", m.Errors())
	}
}
