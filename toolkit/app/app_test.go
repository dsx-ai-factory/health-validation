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

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
	"github.com/NVIDIA/health-validation/toolkit"
)

const testNodeID = "node-123"

// customCheck is a minimal node check used to exercise the WithChecks SPI.
func customCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:              "test_custom_check",
		Name:            "Test Custom Check",
		Category:        health.Node,
		RequiredSignals: []string{"gpu_count"},
		Thresholds: map[string]health.ThresholdSpec{
			"max_count": {
				Value:       int64(8),
				Category:    health.DefaultThreshold,
				Description: "Arbitrary test threshold.",
				Unit:        "count",
			},
		},
		Rules: []health.Rule{
			{Expression: "gpu_count > max_count", Severity: health.Critical, Message: "too many"},
		},
	}
}

// TestWithChecksRegistersCustomCheck confirms a check supplied via the
// WithChecks SPI is registered into the catalog alongside the built-ins.
func TestWithChecksRegistersCustomCheck(t *testing.T) {
	cat, _, err := buildCatalog("", []health.CheckDefinition{customCheck()}, "")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	if _, err := cat.Get("test_custom_check"); err != nil {
		t.Fatalf("custom check not registered: %v", err)
	}
}

// TestWithChecksPreservesProvenance is the pass-through guard: buildCatalog
// must honor a check's author-declared classification verbatim — neither
// stamping nor rejecting it.
func TestWithChecksPreservesProvenance(t *testing.T) {
	canonical := customCheck()
	canonical.Provenance = health.NVIDIA

	cat, _, err := buildCatalog("", []health.CheckDefinition{canonical}, "")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	got, err := cat.Get("test_custom_check")
	if err != nil {
		t.Fatalf("custom check not registered: %v", err)
	}
	if got.Provenance != health.NVIDIA {
		t.Errorf("provenance = %v, want it preserved as %v (no mutation)",
			got.Provenance, health.NVIDIA)
	}
}

// TestWithChecksDuplicateIDFails confirms the only registration constraint:
// a custom check whose ID collides with a built-in (here gpu_count) makes
// catalog construction fail rather than silently shadowing.
func TestWithChecksDuplicateIDFails(t *testing.T) {
	collide := customCheck()
	collide.ID = "gpu_count" // collides with a built-in

	_, _, err := buildCatalog("", []health.CheckDefinition{collide}, "")
	if err == nil {
		t.Fatal("expected duplicate-ID error, got nil")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to mention the duplicate registration", err.Error())
	}
}

// TestBuildCatalogAppliesArchitecture confirms a detected architecture is
// wired into the catalog: a check skips on an architecture it does not cover
// (A100 here) instead of running with the wrong thresholds. With an empty
// architecture the filter stays off, which the other buildCatalog tests
// exercise.
func TestBuildCatalogAppliesArchitecture(t *testing.T) {
	cat, _, err := buildCatalog("", nil, "A100")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}

	v, err := cat.Evaluate("nvlink_bandwidth_count_speed",
		nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18, SpeedMBytesPerSec: 50000})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped=%v reason=%v, want skipped with SIGNAL_UNAVAILABLE_ARCHITECTURE on A100",
			v.Skipped, v.SkipReason)
	}
}

// --- Per-domain backend override locks ------------------------------------

// spyBackend records whether its MeasureGPUs was called. It satisfies all
// three narrow per-domain interfaces (so one value can stand in for the host
// across gpu/node/nvlink), returning no measurements so a full runCommand
// reaches a verdict without hardware. Only MeasureGPUs flips the flag, so it
// pins which domain the GPU sensor actually read from.
type spyBackend struct {
	called *bool
}

func (s spyBackend) MeasureGPUs() ([]health.Measurement, error) {
	*s.called = true
	return nil, nil
}
func (spyBackend) MeasureNode() ([]health.Measurement, error)   { return nil, nil }
func (spyBackend) MeasureNVLink() ([]health.Measurement, error) { return nil, nil }

// xidSpyBackend records whether its Measure was called; it satisfies
// [toolkit.XidBackend].
type xidSpyBackend struct {
	called *bool
}

func (x xidSpyBackend) Measure(context.Context) ([]health.Measurement, error) {
	*x.called = true
	return nil, nil
}

// fakeHost stands in for the platform host backend so these tests run anywhere
// (no NVML, no cgo). One spy backs all three per-domain accessors, mirroring
// the real host where a single NVML backend does. Installed by overwriting
// App.host.
type fakeHost struct {
	b spyBackend
}

func (h fakeHost) gpuBackend() toolkit.GPUBackend       { return h.b }
func (h fakeHost) nodeBackend() toolkit.NodeBackend     { return h.b }
func (h fakeHost) nvlinkBackend() toolkit.NVLinkBackend { return h.b }
func (fakeHost) architecture() (string, error)          { return "", nil }
func (fakeHost) close() error                           { return nil }

// panicHost ensures invalid input returns before touching the host backend.
type panicHost struct{}

func (panicHost) gpuBackend() toolkit.GPUBackend       { panic("unexpected GPU backend access") }
func (panicHost) nodeBackend() toolkit.NodeBackend     { panic("unexpected node backend access") }
func (panicHost) nvlinkBackend() toolkit.NVLinkBackend { panic("unexpected NVLink backend access") }
func (panicHost) architecture() (string, error)        { panic("unexpected architecture discovery") }
func (panicHost) close() error                         { panic("unexpected host backend close") }

func TestRunCommandRejectsInvalidNodeID(t *testing.T) {
	tests := []struct {
		name          string
		builderNodeID string
		args          []string
		withBuilder   bool
	}{
		{name: "whitespace builder value", builderNodeID: " \t ", withBuilder: true},
		{name: "whitespace flag value", args: []string{"--node-id", " \t "}},
		{name: "whitespace flag overrides builder", builderNodeID: testNodeID, withBuilder: true, args: []string{"--node-id", " \t "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New().WithProgramName("test")
			if tt.withBuilder {
				a.WithNodeID(tt.builderNodeID)
			}
			a.hostname = func() (string, error) {
				panic("unexpected hostname lookup")
			}
			a.host = panicHost{}

			var stdout, stderr bytes.Buffer
			args := append([]string{"run"}, tt.args...)
			if code := a.Run(args, &stdout, &stderr); code != exitError {
				t.Errorf("Run exit = %d, want %d", code, exitError)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want no verdict", stdout.String())
			}
			if !strings.Contains(stderr.String(), "--node-id must not be empty") {
				t.Errorf("stderr = %q, want empty node ID error", stderr.String())
			}
		})
	}
}

func TestRunCommandDefaultsNodeIDToHostname(t *testing.T) {
	const hostname = "nvl72d014-T02"
	hostnameCalls := 0
	a := New().WithProgramName("test")
	a.hostname = func() (string, error) {
		hostnameCalls++
		return "  " + hostname + "  ", nil
	}
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: new(bool)})

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCommand exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	if verdict.NodeID != hostname {
		t.Errorf("NodeID = %q, want hostname %q", verdict.NodeID, hostname)
	}
	if hostnameCalls != 1 {
		t.Errorf("hostname lookup calls = %d, want 1", hostnameCalls)
	}
}

func TestRunCommandRejectsInvalidHostname(t *testing.T) {
	lookupErr := errors.New("hostname unavailable")
	tests := []struct {
		hostname func() (string, error)
		name     string
		want     string
	}{
		{
			name: "lookup failure",
			hostname: func() (string, error) {
				return "", lookupErr
			},
			want: lookupErr.Error(),
		},
		{
			name: "empty hostname",
			hostname: func() (string, error) {
				return " \t ", nil
			},
			want: "empty value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New().WithProgramName("test")
			a.hostname = tt.hostname
			a.host = panicHost{}

			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError {
				t.Errorf("Run exit = %d, want %d", code, exitError)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want no verdict", stdout.String())
			}
			if !strings.Contains(stderr.String(), "hostname") ||
				!strings.Contains(stderr.String(), "--node-id") ||
				!strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want hostname error %q with --node-id guidance", stderr.String(), tt.want)
			}
		})
	}
}

func TestVersionDoesNotResolveHostname(t *testing.T) {
	a := New().WithProgramName("test")
	a.hostname = func() (string, error) {
		panic("unexpected hostname lookup")
	}
	a.host = panicHost{}

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"version", "--short"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("Run exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}
}

func TestRunCommandRejectsUnexpectedArguments(t *testing.T) {
	a := New().WithProgramName("test").WithNodeID("builder-node")
	a.host = panicHost{}

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "unexpected", "--node-id", "target"}, &stdout, &stderr); code != exitError {
		t.Errorf("Run exit = %d, want %d", code, exitError)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no verdict", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unexpected argument "unexpected"`) {
		t.Errorf("stderr = %q, want unexpected argument error", stderr.String())
	}
}

func TestRunCommandNodeIDFlagOverridesBuilder(t *testing.T) {
	a := New().WithProgramName("test").WithNodeID("builder-node")
	a.hostname = func() (string, error) {
		panic("unexpected hostname lookup")
	}
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: new(bool)})

	var stdout, stderr bytes.Buffer
	if code := a.runCommand([]string{"--node-id", "  cli-node  "}, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCommand exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	if verdict.NodeID != "cli-node" {
		t.Errorf("NodeID = %q, want %q", verdict.NodeID, "cli-node")
	}
}

func TestRunNodeIDFlagOverridesHostname(t *testing.T) {
	a := New().WithProgramName("test")
	a.hostname = func() (string, error) {
		panic("unexpected hostname lookup")
	}
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: new(bool)})

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", "cli-node"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("Run exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	if verdict.NodeID != "cli-node" {
		t.Errorf("NodeID = %q, want %q", verdict.NodeID, "cli-node")
	}
}

// TestWithGPUBackendOverridesHost confirms a backend supplied via
// WithGPUBackend is the one the GPU sensor reads from, and the host's GPU
// backend is bypassed. This is the consumer-facing pluggability hook.
func TestWithGPUBackendOverridesHost(t *testing.T) {
	var hostGPUCalled, overrideCalled bool

	a := New()
	a.host = fakeHost{b: spyBackend{called: &hostGPUCalled}}
	a.WithGPUBackend(spyBackend{called: &overrideCalled})
	a.WithNodeID(testNodeID)

	var stdout, stderr bytes.Buffer
	// This asserts which backend was read; the verdict severity is irrelevant, so
	// guard only against a hard collection failure (exitError).
	if code := a.runCommand(nil, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d (collection failed), stderr = %q", code, stderr.String())
	}

	if !overrideCalled {
		t.Error("WithGPUBackend override was not collected from")
	}
	if hostGPUCalled {
		t.Error("host GPU backend was used despite a WithGPUBackend override")
	}
}

// TestDefaultUsesHostBackend is the contrast: with no override, the host's GPU
// backend is the source. Together with the test above this pins the per-domain
// override semantics (override one domain, others fall back to the host).
func TestDefaultUsesHostBackend(t *testing.T) {
	var hostGPUCalled bool

	a := New().WithNodeID("  builder-node  ")
	a.hostname = func() (string, error) {
		panic("unexpected hostname lookup")
	}
	a.host = fakeHost{b: spyBackend{called: &hostGPUCalled}}

	var stdout, stderr bytes.Buffer
	// This asserts which backend was read; the verdict severity is irrelevant, so
	// guard only against a hard collection failure (exitError).
	if code := a.runCommand(nil, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d (collection failed), stderr = %q", code, stderr.String())
	}

	if !hostGPUCalled {
		t.Error("default path did not collect from the host GPU backend")
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	if verdict.NodeID != "builder-node" {
		t.Errorf("NodeID = %q, want %q", verdict.NodeID, "builder-node")
	}
}

// --- CLI exit-code policy -------------------------------------------------

// TestExitCodeForVerdict pins the verdict→exit-code policy. exitError (2) is
// unreachable here by design — it belongs to the no-verdict path (TestEvaluate*).
func TestExitCodeForVerdict(t *testing.T) {
	cases := []struct {
		sev  health.Severity
		want int
	}{
		{health.Pass, exitOK},
		{health.Info, exitOK},
		{health.Warning, exitOK},
		{health.Critical, exitUnhealthy},
		{health.Unknown, exitUnhealthy},
	}
	for _, c := range cases {
		nv := health.NodeVerdict{OverallSeverity: c.sev}
		if got := exitCodeForVerdict(nv); got != c.want {
			t.Errorf("exitCodeForVerdict(%v) = %d, want %d", c.sev, got, c.want)
		}
	}
}

// nodeCatalog isolates the exit-code tests from the built-in catalog so the
// produced severity depends only on the test's input, not on built-in defaults
// (e.g. the built-in gpu_count expects 4). It holds just the custom check,
// which is CRITICAL when gpu_count > 8.
func nodeCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty()
	if err := cat.Register(customCheck()); err != nil {
		t.Fatalf("register custom check: %v", err)
	}
	return cat
}

// failingNodeBackend errors at Measure, standing in for a backend that cannot
// read the host at all (e.g. NVML failed to initialize).
type failingNodeBackend struct{}

func (failingNodeBackend) MeasureNode() ([]health.Measurement, error) {
	return nil, errors.New("backend unavailable")
}

// TestEvaluateExitCodes drives evaluate end-to-end across its verdict exit
// codes. The custom check is CRITICAL when gpu_count > 8, so the count picks
// the verdict. A backend that fails to collect no longer aborts the run: the
// failure is degraded to an errored measurement that fails the dependent
// check Critical, so it exits exitUnhealthy (a produced CRITICAL verdict) with
// the verdict still written — not exitError, which is reserved for the case
// where no verdict is produced at all.
func TestEvaluateExitCodes(t *testing.T) {
	cases := []struct {
		sensor toolkit.Sensor
		name   string
		want   int
	}{
		{toolkit.NewNodeSensor(toolkit.MockNode(node.NodeMeasurement{GPUCount: 16})), "critical verdict", exitUnhealthy},
		{toolkit.NewNodeSensor(toolkit.MockNode(node.NodeMeasurement{GPUCount: 8})), "healthy verdict", exitOK},
		{toolkit.NewNodeSensor(failingNodeBackend{}), "collection failure degrades to critical", exitUnhealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := evaluate(nodeCatalog(t), toolkit.DefaultSensorTimeout, "test", testNodeID, &stdout, &stderr, c.sensor)
			if code != c.want {
				t.Fatalf("evaluate = %d, want %d; stderr=%q", code, c.want, stderr.String())
			}

			var verdict health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
				t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
			}
			if verdict.NodeID != testNodeID {
				t.Errorf("NodeID = %q, want %q", verdict.NodeID, testNodeID)
			}
		})
	}
}

func TestWriteVerdictNodeID(t *testing.T) {
	t.Run("stamps a value copy", func(t *testing.T) {
		input := health.NodeVerdict{NodeID: "upstream"}
		var stdout, stderr bytes.Buffer
		if code := writeVerdict(input, "test", "  node-123  ", &stdout, &stderr); code != exitOK {
			t.Fatalf("writeVerdict exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
		}

		var output health.NodeVerdict
		if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
			t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
		}
		if output.NodeID != testNodeID {
			t.Errorf("output NodeID = %q, want %q", output.NodeID, testNodeID)
		}
		if input.NodeID != "upstream" {
			t.Errorf("input NodeID = %q, want the original value", input.NodeID)
		}
	})

	t.Run("rejects whitespace", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		verdict := health.NodeVerdict{NodeID: "upstream"}
		if code := writeVerdict(verdict, "test", " \t ", &stdout, &stderr); code != exitError {
			t.Errorf("writeVerdict exit = %d, want %d", code, exitError)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want no verdict", stdout.String())
		}
		if !strings.Contains(stderr.String(), "node ID is required") {
			t.Errorf("stderr = %q, want required node ID error", stderr.String())
		}
	})
}

// TestWithXidBackendOverridesJournalctl confirms a backend supplied via
// WithXidBackend is the one the Xid sensor reads from, instead of the default
// journalctl source. (runCommand picks the override or journalctl, never both,
// so the override being read proves journalctl was bypassed.)
func TestWithXidBackendOverridesJournalctl(t *testing.T) {
	var xidCalled bool

	a := New()
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: &xidCalled})
	a.WithNodeID(testNodeID)

	var stdout, stderr bytes.Buffer
	// This asserts which backend was read; the verdict severity is irrelevant, so
	// guard only against a hard collection failure (exitError).
	if code := a.runCommand(nil, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d (collection failed), stderr = %q", code, stderr.String())
	}

	if !xidCalled {
		t.Error("WithXidBackend override was not collected from")
	}
}

// erroringHost is a hostBackend whose architecture discovery fails while its
// per-domain backends would still collect — the dangerous case where discovery
// errors (e.g. transiently) but collection would otherwise succeed and be
// evaluated against the wrong architecture's thresholds.
type erroringHost struct {
	b spyBackend
}

func (h erroringHost) gpuBackend() toolkit.GPUBackend       { return h.b }
func (h erroringHost) nodeBackend() toolkit.NodeBackend     { return h.b }
func (h erroringHost) nvlinkBackend() toolkit.NVLinkBackend { return h.b }
func (erroringHost) architecture() (string, error)          { return "", errors.New("no GPUs enumerated") }
func (erroringHost) close() error                           { return nil }

// TestRunCommandArchDiscoveryFailureDrains confirms an architecture-discovery
// error yields a Critical verdict and does NOT collect: a healthy node is not
// silently evaluated against the wrong thresholds, and a dead node does not
// skip its way to PASS. The Critical verdict maps to exitUnhealthy.
func TestRunCommandArchDiscoveryFailureDrains(t *testing.T) {
	var collected bool
	a := New()
	a.hostname = func() (string, error) {
		return "  nvl72d014-T02  ", nil
	}
	a.host = erroringHost{b: spyBackend{called: &collected}}

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("runCommand exit = %d, want exitUnhealthy (Critical discovery verdict); stderr = %q", code, stderr.String())
	}

	var v health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	if v.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want Critical on discovery failure", v.OverallSeverity)
	}
	if v.NodeID != "nvl72d014-T02" {
		t.Errorf("NodeID = %q, want %q", v.NodeID, "nvl72d014-T02")
	}
	if collected {
		t.Error("collected measurements despite discovery failure; must not evaluate against wrong thresholds")
	}
}
