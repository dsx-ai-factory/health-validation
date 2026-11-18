// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

func TestCollectWithOptions_ZeroValueHealthyNode(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	sensor := nodeSensor(4)

	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{}, sensor)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("overall = %v, want PASS", nv.OverallSeverity)
	}
}

func TestCollectMeasurementsWithTimeoutPreservesSensor(t *testing.T) {
	sensor := nodeSensor(4)

	groups := toolkit.CollectMeasurementsWithTimeout(time.Second, sensor)
	if len(groups) != 1 || groups[0].Sensor != "node" || len(groups[0].Measurements) != 1 {
		t.Fatalf("groups = %+v, want one node measurement", groups)
	}
}

func TestCollect_FailingNode(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// Only 3 GPUs visible against the default expected count of 4.
	sensor := nodeSensor(3)

	nv, err := toolkit.Collect(cat, sensor)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}
}

func TestCollectWithOptions_ContinuousCadenceSkipsPrologChecks(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	sensor := staticSensor{name: "gpu", ms: []health.Measurement{gpu.GPUMeasurement{GPUID: "GPU-0"}}}
	cadence := health.Continuous
	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Cadence: &cadence}, sensor)
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]health.SkipReason{}
	for _, v := range nv.SkippedChecks {
		skipped[v.CheckID] = v.SkipReason
	}
	prolog := catalogtest.CheckIDsAtCadence(cat, health.Prolog)
	if len(prolog) == 0 {
		t.Fatal("catalog has no Prolog checks")
	}
	for _, id := range prolog {
		if skipped[id] != health.CadenceMismatch {
			t.Errorf("%s skip reason = %v, want CADENCE_MISMATCH", id, skipped[id])
		}
	}
	if _, ok := skipped["gpu_power_state"]; ok {
		t.Error("CONTINUOUS Collect unexpectedly skipped Continuous check gpu_power_state")
	}
}

// emptySensor is a Sensor that succeeds while observing nothing — the
// shape of a backend that enumerates no entities, or a domain whose sensor
// was never added to the run at all. It reports no error, so nothing is
// degraded into errored measurements and its category simply never
// arrives.
type emptySensor struct{ name string }

func (s emptySensor) Name() string { return s.name }

func (s emptySensor) Measure(context.Context) ([]health.Measurement, error) { return nil, nil }

func (s emptySensor) Errored(error) []health.Measurement { return nil }

// A sensor that collects nothing without erroring must not make its
// domain's checks disappear. Before SENSOR_MISSING those checks were in
// neither check_results nor skipped_checks, so a node with no GPUs at all
// looked exactly like a catalog that never had GPU checks.
func TestCollect_MissingSensorSurfacesAsSkip(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	nv, err := toolkit.Collect(cat, emptySensor{name: "node"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(nv.CheckResults) != 0 {
		t.Errorf("check_results = %d, want 0; nothing was measured", len(nv.CheckResults))
	}
	if len(nv.SkippedChecks) != cat.Len() {
		t.Errorf("skipped_checks = %d, want %d (every registered check)",
			len(nv.SkippedChecks), cat.Len())
	}

	var sensorMissing int
	for _, v := range nv.SkippedChecks {
		if v.SkipReason == health.SensorMissing {
			sensorMissing++
		}
	}
	if sensorMissing == 0 {
		t.Error("no SENSOR_MISSING skips; an uncollected domain is still invisible")
	}
	if len(nv.CheckResults) != 0 {
		t.Errorf("check_results = %+v, want none; no check completed", nv.CheckResults)
	}
}

// The skips are the machine-readable record; an operator watching the
// console needs to be told too, since a domain that never reported
// produces no other output. One warning per missing domain, not per check.
func TestCollect_MissingSensorWarns(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	if _, err := toolkit.Collect(cat, emptySensor{name: "node"}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "no measurement collected for domain") {
		t.Errorf("no warning for the uncollected domains; log was:\n%s", logged)
	}
	for _, domain := range []health.Category{health.Node, health.GPU} {
		if !strings.Contains(logged, "category="+domain.String()) {
			t.Errorf("log does not name the missing %v domain; log was:\n%s", domain, logged)
		}
	}
	// Nothing was collected, so every domain the catalog covers is missing —
	// one line each. Per-check logging would bury the operator in noise.
	if got, want := strings.Count(logged, "no measurement collected for domain"), len(cat.Categories()); got != want {
		t.Errorf("warnings = %d, want %d (one per domain, not per check)", got, want)
	}
}

// failingSensor is a Sensor whose backend is unavailable: Measure always
// errors, and Errored degrades to an errored measurement built by the
// errored func. It exercises Collect's degrade path without hardware; the
// func lets a test target whichever domain's checks it wants to hit.
type failingSensor struct {
	errored func(err error) []health.Measurement
	name    string
}

func (s failingSensor) Name() string { return s.name }

func (s failingSensor) Measure(context.Context) ([]health.Measurement, error) {
	return nil, fmt.Errorf("%s backend unavailable", s.name)
}

func (s failingSensor) Errored(err error) []health.Measurement { return s.errored(err) }

// TestCollect_SensorErrorDegradesToUnknown verifies a sensor that fails
// to collect does not abort the verdict: its failure becomes an Unknown
// verdict for the dependent check, and the underlying error is surfaced
// in the message.
func TestCollect_SensorErrorDegradesToUnknown(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	sensor := failingSensor{
		name: "node",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
		},
	}

	nv, err := toolkit.Collect(cat, sensor)
	if err != nil {
		t.Fatalf("Collect returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Unknown {
		t.Fatalf("overall = %v, want UNKNOWN", nv.OverallSeverity)
	}

	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Unknown {
			t.Errorf("gpu_count severity = %v, want UNKNOWN", v.Severity)
		}
		if len(v.Messages) == 0 || !strings.Contains(v.Messages[0], "node backend unavailable") {
			t.Errorf("gpu_count messages = %v, want the surfaced sensor error", v.Messages)
		}
	}
	if !found {
		t.Fatal("gpu_count verdict not found; the errored node measurement did not drive the check")
	}
}

// TestCollect_FailingSensorDoesNotMaskHealthy verifies a failing sensor
// in one domain does not suppress a healthy sensor in another: the
// healthy domain still evaluates to PASS while the failing one is
// Unknown.
func TestCollect_FailingSensorDoesNotMaskHealthy(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	healthy := nodeSensor(4)
	failing := failingSensor{
		name: "nvlink",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals()...)}
		},
	}

	nv, err := toolkit.Collect(cat, healthy, failing)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if got := nv.SeverityByDomain[health.Node]; got != health.Pass {
		t.Errorf("node domain = %v, want PASS (healthy sensor must not be masked)", got)
	}
	if got := nv.SeverityByDomain[health.NVLink]; got != health.Unknown {
		t.Errorf("nvlink domain = %v, want UNKNOWN (degraded)", got)
	}
	if nv.OverallSeverity != health.Unknown {
		t.Errorf("overall = %v, want UNKNOWN", nv.OverallSeverity)
	}
}

// blockingSensor's Measure blocks to model a slow or wedged data source.
// With honorCtx it returns when the context is cancelled; otherwise it
// ignores the context entirely (modeling an uninterruptible NVML/cgo
// call) and returns only when release is closed, so the watchdog is the
// only thing that can end the wait. errored builds the degraded
// measurements, letting a test pick which domain's checks the failure
// hits.
type blockingSensor struct {
	errored  func(err error) []health.Measurement
	release  chan struct{}
	name     string
	honorCtx bool
}

func (s *blockingSensor) Name() string { return s.name }

func (s *blockingSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	if s.honorCtx {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.release:
			return nil, nil
		}
	}
	<-s.release
	return nil, nil
}

func (s *blockingSensor) Errored(err error) []health.Measurement { return s.errored(err) }

// TestCollectWithOptions_UninterruptibleSensorDoesNotHang is the core
// safety property: a sensor whose Measure ignores cancellation (as a
// wedged cgo call would) must not hang the run — the watchdog ends the
// wait and degrades the sensor to Unknown.
func TestCollectWithOptions_UninterruptibleSensorDoesNotHang(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := &blockingSensor{
		name:     "node",
		honorCtx: false,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
		},
	}
	// Let the parked goroutine exit once the watchdog has given up.
	defer close(s.release)

	done := make(chan health.NodeVerdict, 1)
	go func() {
		nv, _ := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, s)
		done <- nv
	}()

	select {
	case nv := <-done:
		if nv.OverallSeverity != health.Unknown {
			t.Errorf("overall = %v, want UNKNOWN", nv.OverallSeverity)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithOptions hung on a sensor that ignores cancellation")
	}
}

// TestCollectWithOptions_TimeoutDegradesToUnknown verifies a timed-out
// sensor produces an Unknown verdict whose message surfaces the deadline,
// so a timeout is distinguishable from a healthy or otherwise-failed
// check.
func TestCollectWithOptions_TimeoutDegradesToUnknown(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := &blockingSensor{
		name:     "node",
		honorCtx: true,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
		},
	}
	defer close(s.release)

	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, s)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Unknown {
		t.Fatalf("overall = %v, want UNKNOWN", nv.OverallSeverity)
	}

	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Unknown {
			t.Errorf("gpu_count severity = %v, want UNKNOWN", v.Severity)
		}
		if len(v.Messages) == 0 || !strings.Contains(v.Messages[0], health.ErrCollectionTimeout.Error()) {
			t.Errorf("gpu_count messages = %v, want the surfaced collection timeout", v.Messages)
		}
	}
	if !found {
		t.Fatal("gpu_count verdict not found; the timed-out node measurement did not drive the check")
	}
}

// TestCollectWithOptions_TimeoutDoesNotMaskHealthy verifies a timed-out
// sensor in one domain does not suppress a healthy sensor in another.
func TestCollectWithOptions_TimeoutDoesNotMaskHealthy(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	healthy := nodeSensor(4)
	slow := &blockingSensor{
		name:     "nvlink",
		honorCtx: true,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals()...)}
		},
	}
	defer close(slow.release)

	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, healthy, slow)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error: %v", err)
	}
	if got := nv.SeverityByDomain[health.Node]; got != health.Pass {
		t.Errorf("node domain = %v, want PASS (healthy sensor must not be masked)", got)
	}
	if got := nv.SeverityByDomain[health.NVLink]; got != health.Unknown {
		t.Errorf("nvlink domain = %v, want UNKNOWN (timed out)", got)
	}
}

// panicSensor's Measure panics, modeling a backend that faults in Go code
// (e.g. an out-of-range parse or an explicit panic). Collect must degrade
// it to an Unknown verdict, not crash the whole run.
type panicSensor struct {
	errored func(err error) []health.Measurement
	name    string
}

func (s panicSensor) Name() string { return s.name }

func (s panicSensor) Measure(context.Context) ([]health.Measurement, error) {
	panic("backend blew up")
}

func (s panicSensor) Errored(err error) []health.Measurement { return s.errored(err) }

// TestCollectWithOptions_SensorPanicDegradesToUnknown verifies a panicking
// Measure is recovered and degraded, not allowed to crash the process.
func TestCollectWithOptions_SensorPanicDegradesToUnknown(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := panicSensor{
		name: "node",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
		},
	}

	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: time.Second}, s)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Unknown {
		t.Fatalf("overall = %v, want UNKNOWN", nv.OverallSeverity)
	}
	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Unknown {
			t.Errorf("gpu_count severity = %v, want UNKNOWN", v.Severity)
		}
		if len(v.Messages) == 0 || !strings.Contains(v.Messages[0], "panicked") {
			t.Errorf("gpu_count messages = %v, want the recovered panic surfaced", v.Messages)
		}
	}
	if !found {
		t.Fatal("gpu_count verdict not found; the panicking sensor did not degrade to a verdict")
	}
}
