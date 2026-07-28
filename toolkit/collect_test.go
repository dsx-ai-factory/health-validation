/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package toolkit_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestCollect_HealthyNode(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 4},
	))

	nv, err := toolkit.Collect(cat, sensor)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("overall = %v, want PASS", nv.OverallSeverity)
	}
	if nv.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", nv.ExitCode())
	}
}

func TestCollect_FailingNode(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// Only 3 GPUs visible against the default expected count of 4.
	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 3},
	))

	nv, err := toolkit.Collect(cat, sensor)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}
	if nv.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", nv.ExitCode())
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

// TestCollect_SensorErrorDegradesToCritical verifies a sensor that fails
// to collect does not abort the verdict: its failure becomes a Critical
// verdict for the dependent check, and the underlying error is surfaced
// in the message.
func TestCollect_SensorErrorDegradesToCritical(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	sensor := failingSensor{
		name: "node",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
		},
	}

	nv, err := toolkit.Collect(cat, sensor)
	if err != nil {
		t.Fatalf("Collect returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Fatalf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}

	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Critical {
			t.Errorf("gpu_count severity = %v, want CRITICAL", v.Severity)
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
// Critical.
func TestCollect_FailingSensorDoesNotMaskHealthy(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	healthy := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 4},
	))
	failing := failingSensor{
		name: "nvlink",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals...)}
		},
	}

	nv, err := toolkit.Collect(cat, healthy, failing)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if got := nv.SeverityByDomain[health.Node]; got != health.Pass {
		t.Errorf("node domain = %v, want PASS (healthy sensor must not be masked)", got)
	}
	if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
		t.Errorf("nvlink domain = %v, want CRITICAL (degraded)", got)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}
}

// blockingSensor's Measure blocks to model a slow or wedged data source.
// With honorCtx it returns when the context is cancelled; otherwise it
// ignores the context entirely (modeling an uninterruptible NVML/cgo
// call) and returns only when release is closed, so the watchdog is the
// only thing that can end the wait. errored builds the degraded
// measurements, letting a test pick which domain's checks the failure
// hits. key, when set, makes the sensor share a backend resource (see
// resourceKeyed); measured, when set, is closed on Measure entry so a
// test can detect a sensor that should instead have been skipped.
type blockingSensor struct {
	errored  func(err error) []health.Measurement
	release  chan struct{}
	measured chan struct{}
	name     string
	key      string
	honorCtx bool
}

func (s *blockingSensor) Name() string { return s.name }

func (s *blockingSensor) ResourceKey() string { return s.key }

func (s *blockingSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	if s.measured != nil {
		close(s.measured)
	}
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

// TestCollectWithTimeout_UninterruptibleSensorDoesNotHang is the core
// safety property: a sensor whose Measure ignores cancellation (as a
// wedged cgo call would) must not hang the run — the watchdog ends the
// wait and degrades the sensor to Critical.
func TestCollectWithTimeout_UninterruptibleSensorDoesNotHang(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := &blockingSensor{
		name:     "node",
		honorCtx: false,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
		},
	}
	// Let the parked goroutine exit once the watchdog has given up.
	defer close(s.release)

	done := make(chan health.NodeVerdict, 1)
	go func() {
		nv, _ := toolkit.CollectWithTimeout(cat, 20*time.Millisecond, s)
		done <- nv
	}()

	select {
	case nv := <-done:
		if nv.OverallSeverity != health.Critical {
			t.Errorf("overall = %v, want CRITICAL", nv.OverallSeverity)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithTimeout hung on a sensor that ignores cancellation")
	}
}

// TestCollectWithTimeout_TimeoutDegradesToCritical verifies a timed-out
// sensor produces a Critical verdict whose message surfaces the deadline,
// so a timeout is distinguishable from a healthy or otherwise-failed
// check.
func TestCollectWithTimeout_TimeoutDegradesToCritical(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := &blockingSensor{
		name:     "node",
		honorCtx: true,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
		},
	}
	defer close(s.release)

	nv, err := toolkit.CollectWithTimeout(cat, 20*time.Millisecond, s)
	if err != nil {
		t.Fatalf("CollectWithTimeout returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Fatalf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}

	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Critical {
			t.Errorf("gpu_count severity = %v, want CRITICAL", v.Severity)
		}
		if len(v.Messages) == 0 || !strings.Contains(v.Messages[0], health.ErrCollectionTimeout.Error()) {
			t.Errorf("gpu_count messages = %v, want the surfaced collection timeout", v.Messages)
		}
	}
	if !found {
		t.Fatal("gpu_count verdict not found; the timed-out node measurement did not drive the check")
	}
}

// TestCollectWithTimeout_TimeoutDoesNotMaskHealthy verifies a timed-out
// sensor in one domain does not suppress a healthy sensor in another.
func TestCollectWithTimeout_TimeoutDoesNotMaskHealthy(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	healthy := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 4},
	))
	slow := &blockingSensor{
		name:     "nvlink",
		honorCtx: true,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals...)}
		},
	}
	defer close(slow.release)

	nv, err := toolkit.CollectWithTimeout(cat, 20*time.Millisecond, healthy, slow)
	if err != nil {
		t.Fatalf("CollectWithTimeout returned error: %v", err)
	}
	if got := nv.SeverityByDomain[health.Node]; got != health.Pass {
		t.Errorf("node domain = %v, want PASS (healthy sensor must not be masked)", got)
	}
	if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
		t.Errorf("nvlink domain = %v, want CRITICAL (timed out)", got)
	}
}

// TestCollectWithTimeout_CircuitBreakerSkipsWedgedResource verifies that
// once a sensor times out, a later sensor sharing the same backend
// resource is skipped — its Measure never runs — and degraded directly.
func TestCollectWithTimeout_CircuitBreakerSkipsWedgedResource(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// first shares resource "nvml" and wedges (ignores cancellation), so
	// the watchdog must time it out.
	first := &blockingSensor{
		name:     "node",
		key:      "nvml",
		honorCtx: false,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
		},
	}
	defer close(first.release)

	// second shares the same resource; once first wedges it must be
	// skipped without its Measure ever running (which would close
	// secondMeasured).
	secondMeasured := make(chan struct{})
	second := &blockingSensor{
		name:     "nvlink",
		key:      "nvml",
		honorCtx: false,
		release:  make(chan struct{}),
		measured: secondMeasured,
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals...)}
		},
	}
	defer close(second.release)

	done := make(chan health.NodeVerdict, 1)
	go func() {
		nv, _ := toolkit.CollectWithTimeout(cat, 20*time.Millisecond, first, second)
		done <- nv
	}()

	select {
	case nv := <-done:
		select {
		case <-secondMeasured:
			t.Error("second sensor was measured; it should have been skipped after the shared resource timed out")
		default:
		}
		if got := nv.SeverityByDomain[health.Node]; got != health.Critical {
			t.Errorf("node domain = %v, want CRITICAL (first sensor timed out)", got)
		}
		if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
			t.Errorf("nvlink domain = %v, want CRITICAL (skipped via circuit breaker)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithTimeout hung")
	}
}

// TestCollectWithTimeout_CircuitBreakerScopedToResource verifies the
// breaker is per-resource: a wedged backend does not skip a sensor on a
// different backend, which still collects normally.
func TestCollectWithTimeout_CircuitBreakerScopedToResource(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	wedged := &blockingSensor{
		name:     "nvlink",
		key:      "nvml",
		honorCtx: false,
		release:  make(chan struct{}),
		errored: func(err error) []health.Measurement {
			return []health.Measurement{nvlink.ErroredNVLink("nvlink", err, nvlink.MeasurementSignals...)}
		},
	}
	defer close(wedged.release)

	// A healthy sensor on a different backend (its own resource key) must
	// still be collected despite the wedge.
	healthy := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 4},
	))

	done := make(chan health.NodeVerdict, 1)
	go func() {
		nv, _ := toolkit.CollectWithTimeout(cat, 20*time.Millisecond, wedged, healthy)
		done <- nv
	}()

	select {
	case nv := <-done:
		if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
			t.Errorf("nvlink domain = %v, want CRITICAL (wedged)", got)
		}
		if got := nv.SeverityByDomain[health.Node]; got != health.Pass {
			t.Errorf("node domain = %v, want PASS (different resource must still be collected)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithTimeout hung")
	}
}

// TestSensorsShareResourceKeyForSharedBackend verifies the wiring behind
// the circuit breaker: the GPU, Node, and NVLink sensors report the same
// resource key when they share a backend, and a different one otherwise.
// The shared source is a [toolkit.LazyGPUBackend] (a single MultiBackend
// installed under all three sensors), mirroring the production wiring where
// one lazily-constructed NVML backend feeds every GPU-side sensor.
func TestSensorsShareResourceKeyForSharedBackend(t *testing.T) {
	backend := toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) {
		return newNodeMultiBackend(node.NodeMeasurement{GPUCount: 4}), nil
	})
	gpuSensor := toolkit.NewGPUSensor(backend)
	nodeSensor := toolkit.NewNodeSensor(backend)
	nvlinkSensor := toolkit.NewNVLinkSensor(backend)

	key := gpuSensor.ResourceKey()
	if key == "" {
		t.Fatal("GPU sensor reported an empty resource key")
	}
	if nodeSensor.ResourceKey() != key || nvlinkSensor.ResourceKey() != key {
		t.Errorf("sensors sharing a backend differ: gpu=%q node=%q nvlink=%q",
			key, nodeSensor.ResourceKey(), nvlinkSensor.ResourceKey())
	}

	other := toolkit.NewNodeSensor(toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) {
		return newNodeMultiBackend(node.NodeMeasurement{GPUCount: 8}), nil
	}))
	if other.ResourceKey() == key {
		t.Errorf("sensors on different backends share key %q", key)
	}
}

// panicSensor's Measure panics, modeling a backend that faults in Go code
// (e.g. an out-of-range parse or an explicit panic). Collect must degrade
// it to a Critical verdict, not crash the whole run.
type panicSensor struct {
	errored func(err error) []health.Measurement
	name    string
}

func (s panicSensor) Name() string { return s.name }

func (s panicSensor) Measure(context.Context) ([]health.Measurement, error) {
	panic("backend blew up")
}

func (s panicSensor) Errored(err error) []health.Measurement { return s.errored(err) }

// TestCollectWithTimeout_SensorPanicDegradesToCritical verifies a panicking
// Measure is recovered and degraded, not allowed to crash the process.
func TestCollectWithTimeout_SensorPanicDegradesToCritical(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	s := panicSensor{
		name: "node",
		errored: func(err error) []health.Measurement {
			return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
		},
	}

	nv, err := toolkit.CollectWithTimeout(cat, time.Second, s)
	if err != nil {
		t.Fatalf("CollectWithTimeout returned error, want graceful degrade: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Fatalf("overall = %v, want CRITICAL", nv.OverallSeverity)
	}
	var found bool
	for _, v := range nv.CheckResults {
		if v.CheckID != "gpu_count" {
			continue
		}
		found = true
		if v.Severity != health.Critical {
			t.Errorf("gpu_count severity = %v, want CRITICAL", v.Severity)
		}
		if len(v.Messages) == 0 || !strings.Contains(v.Messages[0], "panicked") {
			t.Errorf("gpu_count messages = %v, want the recovered panic surfaced", v.Messages)
		}
	}
	if !found {
		t.Fatal("gpu_count verdict not found; the panicking sensor did not degrade to a verdict")
	}
}
