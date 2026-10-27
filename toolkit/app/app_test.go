// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

const testNodeID = "node-123"

func TestCollectionCategoriesWithoutConfigIncludesEveryBuiltinDomain(t *testing.T) {
	cat := catalog.New()
	categories, err := collectionCategories(cat, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != len(cat.Categories()) {
		t.Fatalf("categories = %v, want every built-in domain %v", categories, cat.Categories())
	}
}

func TestCollectionCategories(t *testing.T) {
	categories, err := collectionCategories(catalog.New(), &Config{
		Checks: map[string]map[string]any{
			"gpu_count":     {},
			"nvlink_active": {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 2 || !categories[health.Node] || !categories[health.NVLink] {
		t.Fatalf("categories = %v, want node and nvlink", categories)
	}
	if categories[health.GPU] {
		t.Fatal("unselected GPU category included")
	}
}

func TestCollectionOutputEmitsObservationBatch(t *testing.T) {
	partial := gpu.GPUMeasurement{GPUID: "GPU-0", PowerLimitW: 700}
	partial.AddError(errors.New("pstate unavailable"), gpu.SignalGPUPstate)
	batch, err := collectionOutput("cycle-1", testNodeID, []toolkit.SensorMeasurements{
		{Sensor: "gpu", Measurements: []health.Measurement{partial}},
		{Sensor: "cpu", Measurements: []health.Measurement{cpu.CPUMeasurement{PossibleCPUCount: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.GetCollectionId() != "cycle-1" {
		t.Errorf("collection_id = %q", batch.GetCollectionId())
	}
	bySignal := make(map[string]*observation.Observation)
	for _, o := range batch.GetObservations() {
		bySignal[o.GetSignalId()] = o
	}
	if o := bySignal["gpu.power_limit_w"]; o.GetValue().GetDoubleValue() != 700 || o.GetSource() != "toolkit.gpu" {
		t.Errorf("power limit = %v", o)
	}
	if bySignal["gpu.gpu_pstate"].GetCollectionError() == nil {
		t.Errorf("pstate = %v, want a collection error", bySignal["gpu.gpu_pstate"])
	}
	if o := bySignal["cpu.possible_cpu_count"]; o.GetValue().GetIntValue() != 1 || o.GetTarget().GetNode().GetId() != testNodeID {
		t.Errorf("possible CPU count = %v, want 1 targeting node %q", o, testNodeID)
	}
}

func TestCollectCommandWritesObservationBatch(t *testing.T) {
	a := New().WithProgramName("test")
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithGPUBackend(toolkit.MockGPU(gpu.GPUMeasurement{GPUID: "GPU-0", PowerLimitW: 700, EnforcedPowerLimitW: 700}))
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 1}))

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"collect", "--node-id", testNodeID}, &stdout, &stderr); code != exitOK {
		t.Fatalf("collect exit = %d; stderr = %q", code, stderr.String())
	}
	var batch observation.ObservationBatch
	if err := protojson.Unmarshal(stdout.Bytes(), &batch); err != nil {
		t.Fatalf("stdout is not an ObservationBatch: %v\n%s", err, stdout.String())
	}
	if err := observation.ValidateBatch(&batch); err != nil {
		t.Fatal(err)
	}
	if batch.GetCollectionId() == "" {
		t.Fatalf("missing collection ID:\n%s", stdout.String())
	}
	for _, want := range []func(*observation.Observation) bool{
		func(o *observation.Observation) bool {
			return o.GetSignalId() == "gpu.power_limit_w" && o.GetValue().GetDoubleValue() == 700
		},
		func(o *observation.Observation) bool {
			return o.GetSignalId() == "cpu.numa_node_count" && o.GetTarget().GetNode().GetId() == testNodeID
		},
	} {
		if !slices.ContainsFunc(batch.GetObservations(), want) {
			t.Errorf("output is missing an expected observation:\n%s", stdout.String())
		}
	}

	// The written batch rebuilds the measurements an evaluate step needs.
	var rebuilt *gpu.GPUMeasurement
	for _, m := range observations.ToMeasurements(batch.GetObservations()) {
		if g, ok := m.(gpu.GPUMeasurement); ok {
			rebuilt = &g
		}
	}
	if rebuilt == nil || rebuilt.GPUID != "GPU-0" || rebuilt.PowerLimitW != 700 || len(rebuilt.Errors()) != 0 {
		t.Fatalf("rebuilt GPU measurement = %+v", rebuilt)
	}
}

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
				ValueType:   health.ThresholdInt64,
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
	cat, _, err := buildCatalog(nil, []health.CheckDefinition{customCheck()}, "", health.OperatorInfra)
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

	cat, _, err := buildCatalog(nil, []health.CheckDefinition{canonical}, "", health.OperatorInfra)
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

	_, _, err := buildCatalog(nil, []health.CheckDefinition{collide}, "", health.OperatorInfra)
	if err == nil {
		t.Fatal("expected duplicate-ID error, got nil")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to mention the duplicate registration", err.Error())
	}
}

// TestBuildCatalogAppliesArchitecture confirms a detected architecture is
// wired into the catalog: a check skips on an architecture it does not cover
// (A100 here) instead of running with the wrong thresholds.
func TestBuildCatalogAppliesArchitecture(t *testing.T) {
	cat, _, err := buildCatalog(nil, nil, "A100", health.OperatorInfra)
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

type cpuBackendFunc func(context.Context) (cpu.CPUMeasurement, error)

func (f cpuBackendFunc) MeasureCPU(ctx context.Context) (cpu.CPUMeasurement, error) {
	return f(ctx)
}

// xidSpyBackend records whether its Measure was called; it satisfies
// [toolkit.XidBackend].
type xidSpyBackend struct {
	called *bool
}

type ibSpyBackend struct {
	called *bool
}

func (b ibSpyBackend) MeasureIB(context.Context) ([]health.Measurement, error) {
	*b.called = true
	return nil, nil
}

func (ibSpyBackend) Errored(error) []health.Measurement { return nil }

func (x xidSpyBackend) Measure(context.Context) ([]health.Measurement, error) {
	*x.called = true
	return nil, nil
}

// fakeHost stands in for the platform host backend so these tests run anywhere
// (no NVML, no cgo). One spy backs both legacy per-domain accessors,
// mirroring the real host where a single NVML backend does. Installed by
// overwriting App.host.
//
// Its inventory is a healthy GB200 node: runCommand Evaluates the full builtin
// catalog, so discovery must find a real generation.
// catalogtest.DummyArchitecture would skip scoped checks (see
// TestNUMANodeCountReachesVerdict).
type fakeHost struct {
	b spyBackend
}

func (h fakeHost) gpuBackend() toolkit.GPUBackend       { return h.b }
func (h fakeHost) nvlinkBackend() toolkit.NVLinkBackend { return h.b }
func (fakeHost) gpuInventory() *gpuinventory.Inventory {
	return gpuinventory.New(func() (gpuinventory.Reader, error) { return inventoryReader{gpus: 4, model: "NVIDIA GB200"}, nil })
}
func (fakeHost) close() error { return nil }

// inventoryReader reports a basic inventory of identical GPUs without NVML.
type inventoryReader struct {
	model string
	gpus  int
}

func (inventoryReader) ID() string { return "fake" }

func (r inventoryReader) Read(context.Context) (*observation.GPUInventorySnapshot, error) {
	inventory := &observation.GPUInventory{}
	for i := range r.gpus {
		uuid, model := fmt.Sprintf("GPU-%d", i), r.model
		inventory.Entries = append(inventory.Entries, &observation.GPUInventoryEntry{SourceIndex: uint32(i), Gpu: &observation.GPUDevice{Uuid: &uuid, Model: &model}})
	}
	return &observation.GPUInventorySnapshot{Source: "fake", Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: inventory}}, nil
}

// panicHost ensures invalid input returns before touching the host backend.
type panicHost struct{}

func (panicHost) gpuBackend() toolkit.GPUBackend        { panic("unexpected GPU backend access") }
func (panicHost) nvlinkBackend() toolkit.NVLinkBackend  { panic("unexpected NVLink backend access") }
func (panicHost) gpuInventory() *gpuinventory.Inventory { panic("unexpected GPU inventory access") }
func (panicHost) close() error                          { panic("unexpected host backend close") }

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
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	a.WithIBBackend(ibSpyBackend{called: new(bool)})

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
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	a.WithIBBackend(ibSpyBackend{called: new(bool)})

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
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	a.WithIBBackend(ibSpyBackend{called: new(bool)})

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
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))

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
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))

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

func TestWithCPUBackendOverridesSysfs(t *testing.T) {
	var cpuCalled bool
	a := New()
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithCPUBackend(cpuBackendFunc(func(context.Context) (cpu.CPUMeasurement, error) {
		cpuCalled = true
		return cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 8, NUMANodeCount: 34}, nil
	}))
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithIBBackend(ibSpyBackend{called: new(bool)})

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCommand exit = %d, want exitOK; stderr = %q", code, stderr.String())
	}
	if !cpuCalled {
		t.Fatal("WithCPUBackend override was not collected from")
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}

	var matches []health.CheckVerdict
	for _, result := range verdict.CheckResults {
		if result.CheckID == "cpu_count" {
			matches = append(matches, result)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("cpu_count results = %d, want 1", len(matches))
	}
	result := matches[0]
	if result.Category != health.CPU {
		t.Errorf("Category = %v, want CPU", result.Category)
	}
	if result.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS", result.Severity)
	}
	if got := result.ObservedValues[cpu.SignalPossibleCPUCount]; got != float64(8) {
		t.Errorf("possible_cpu_count = %v, want 8", got)
	}
	if got := result.ObservedValues[cpu.SignalOnlineCPUCount]; got != float64(8) {
		t.Errorf("online_cpu_count = %v, want 8", got)
	}
}

// The CPU sensor feeds two checks from one measurement. Pin that a NUMA
// topology mismatch reaches the emitted verdict through the real run path,
// which a direct catalog evaluation cannot establish.
func TestNUMANodeCountReachesVerdict(t *testing.T) {
	a := New()
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{
		PossibleCPUCount: 8,
		OnlineCPUCount:   8,
		NUMANodeCount:    33,
	}))
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("runCommand exit = %d, want %d; stderr = %q", code, exitUnhealthy, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}

	var matches []health.CheckVerdict
	for _, result := range verdict.CheckResults {
		if result.CheckID == "numa_node_count" {
			matches = append(matches, result)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("numa_node_count results = %d, want 1", len(matches))
	}
	result := matches[0]
	if result.Category != health.CPU {
		t.Errorf("Category = %v, want CPU", result.Category)
	}
	if result.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL (33 != expected 34)", result.Severity)
	}
	if !result.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("RecommendedAction = %v, want generic reset/restart/escalate ladder", result.RecommendedAction)
	}
	if got := result.ObservedValues[cpu.SignalNUMANodeCount]; got != float64(33) {
		t.Errorf("numa_node_count = %v, want 33", got)
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
	cat := catalog.NewEmpty(catalog.WithArchitecture(catalogtest.DummyArchitecture))
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
			_, _, code := evaluate(nodeCatalog(t), toolkit.CollectionOptions{}, "test", testNodeID, &stdout, &stderr, c.sensor)
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
		input := health.NodeVerdict{NodeID: "upstream", CatalogVersion: "upstream"}
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
		if got, want := output.CatalogVersion, "upstream"; got != want {
			t.Errorf("output CatalogVersion = %q, want %q", got, want)
		}
		if input.NodeID != "upstream" {
			t.Errorf("input NodeID = %q, want the original value", input.NodeID)
		}
		if input.CatalogVersion != "upstream" {
			t.Errorf("input CatalogVersion = %q, want the original value", input.CatalogVersion)
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
// collect sensor. (runCommand uses the override or the collect sensor, never
// both, so the override being read proves the journal was bypassed.)
func TestWithXidBackendOverridesJournalctl(t *testing.T) {
	var xidCalled bool

	a := New()
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	a.WithXidBackend(xidSpyBackend{called: &xidCalled})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
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

// The IB sensor is opt-in: supplied a backend via WithIBBackend, it collects
// with no config needed. (Without a backend it does not run at all.)
func TestIBSensorCollectsWithBackend(t *testing.T) {
	var ibCalled bool
	a := New().WithNodeID(testNodeID)
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithIBBackend(ibSpyBackend{called: &ibCalled})

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(nil, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
	if !ibCalled {
		t.Fatal("InfiniBand sensor did not collect")
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
func (h erroringHost) nvlinkBackend() toolkit.NVLinkBackend { return h.b }
func (erroringHost) gpuInventory() *gpuinventory.Inventory {
	return gpuinventory.New(func() (gpuinventory.Reader, error) { return inventoryReader{}, nil })
}
func (erroringHost) close() error { return nil }

// TestRunCommandArchDiscoveryFailureDrains confirms an architecture-discovery
// error yields a Critical verdict and does NOT collect: a healthy node is not
// silently evaluated against the wrong thresholds, and a dead node does not
// skip its way to PASS. The Critical verdict maps to exitUnhealthy.
func TestRunCommandArchDiscoveryFailureDrains(t *testing.T) {
	var collected, cpuCollected bool
	a := New()
	a.hostname = func() (string, error) {
		return "  nvl72d014-T02  ", nil
	}
	a.host = erroringHost{b: spyBackend{called: &collected}}
	a.WithCPUBackend(cpuBackendFunc(func(context.Context) (cpu.CPUMeasurement, error) {
		cpuCollected = true
		return cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1}, nil
	}))

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
	// The synthetic verdict has no CheckDefinition to copy a scope from, so it is
	// set by hand. Unenumerable GPUs are a node-local condition.
	if len(v.CheckResults) != 1 {
		t.Fatalf("CheckResults = %d, want 1", len(v.CheckResults))
	}
	if got := v.CheckResults[0]; got.CheckID != "architecture_discovery" || got.ConditionScope != health.NodeScope {
		t.Errorf("check result = {id:%q scope:%v}, want {architecture_discovery NODE}", got.CheckID, got.ConditionScope)
	}
	if collected {
		t.Error("collected measurements despite discovery failure; must not evaluate against wrong thresholds")
	}
	if cpuCollected {
		t.Error("collected CPU measurements despite discovery failure")
	}
}

func verdictHasCheck(nv health.NodeVerdict, id string) bool {
	for _, v := range nv.CheckResults {
		if v.CheckID == id {
			return true
		}
	}
	for _, v := range nv.SkippedChecks {
		if v.CheckID == id {
			return true
		}
	}
	return false
}

func cadenceRunApp() *App {
	a := New().WithProgramName("test")
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	return a
}

func TestRunCommandRejectsInvalidCadence(t *testing.T) {
	a := New().WithProgramName("test")
	a.host = panicHost{}
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID, "--cadence", "NOT_A_CADENCE"}, &stdout, &stderr); code != exitError {
		t.Fatalf("Run exit = %d, want %d; stderr = %q", code, exitError, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no verdict", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown scheduling cadence") {
		t.Errorf("stderr = %q, want unknown cadence error", stderr.String())
	}
}

func TestRunCommandRejectsInvalidExecutionContext(t *testing.T) {
	a := New().WithProgramName("test")
	a.host = panicHost{}
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID, "--execution-context", "NOT_A_CONTEXT"}, &stdout, &stderr); code != exitError {
		t.Fatalf("Run exit = %d, want %d; stderr = %q", code, exitError, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no verdict", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown execution context") {
		t.Errorf("stderr = %q, want unknown execution context error", stderr.String())
	}
}

func TestRunCommandExecutionContextSelection(t *testing.T) {
	check := customCheck()
	check.ID = "tenant_only"
	check.ExecutionContexts = []health.ExecutionContext{health.TenantGuest}
	tests := []struct {
		name        string
		args        []string
		wantSkipped bool
	}{
		{name: "default is tenant guest"},
		{name: "operator infra flag", args: []string{"--execution-context", "OPERATOR_INFRA"}, wantSkipped: true},
		{name: "folded and padded tenant guest", args: []string{"--execution-context", "  tenant_guest  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := cadenceRunApp().WithChecks(check).WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{GPUCount: 4}))
			args := append([]string{"--node-id", testNodeID}, tt.args...)
			var stdout, stderr bytes.Buffer
			if code := a.runCommand(args, &stdout, &stderr); code == exitError {
				t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
			}
			var verdict health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
				t.Fatalf("unmarshal: %v; stdout = %s", err, stdout.String())
			}

			var found, skipped bool
			for _, result := range verdict.CheckResults {
				if result.CheckID == check.ID {
					found = true
				}
			}
			for _, result := range verdict.SkippedChecks {
				if result.CheckID == check.ID {
					found = true
					skipped = true
					if result.SkipReason != health.DeploymentUnsupported {
						t.Errorf("%s skip reason = %v, want %v", check.ID, result.SkipReason, health.DeploymentUnsupported)
					}
				}
			}
			if !found {
				t.Fatalf("%s verdict not found", check.ID)
			}
			if skipped != tt.wantSkipped {
				t.Errorf("%s skipped = %v, want %v", check.ID, skipped, tt.wantSkipped)
			}
		})
	}
}

func TestRunCommandCadenceAcceptsFoldedAndPaddedNames(t *testing.T) {
	a := cadenceRunApp()
	for _, name := range []string{"continuous", "  CONTINUOUS  "} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := a.runCommand([]string{"--node-id", testNodeID, "--cadence", name}, &stdout, &stderr); code == exitError {
				t.Fatalf("runCommand --cadence %q exit = %d; stderr = %q", name, code, stderr.String())
			}
			var v health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
				t.Fatalf("unmarshal: %v; stdout = %s", err, stdout.String())
			}
			if !verdictHasCheck(v, "gpu_count") {
				t.Error("missing Continuous check gpu_count")
			}
		})
	}
}

func TestRunCommandDefaultIncludesPrologChecks(t *testing.T) {
	a := cadenceRunApp()
	var stdout, stderr bytes.Buffer
	if code := a.runCommand([]string{"--node-id", testNodeID}, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
	var v health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal: %v; stdout = %s", err, stdout.String())
	}
	prolog := catalogtest.CheckIDsAtCadence(catalog.New(), health.Prolog)
	if len(prolog) == 0 {
		t.Fatal("builtin catalog has no Prolog checks")
	}
	for _, id := range prolog {
		if !verdictHasCheck(v, id) {
			t.Errorf("default run missing %s; unfiltered default evaluates all registered checks", id)
		}
	}
}

func TestRunCommandCadenceContinuousSkipsPrologChecks(t *testing.T) {
	a := cadenceRunApp()
	var stdout, stderr bytes.Buffer
	if code := a.runCommand([]string{"--node-id", testNodeID, "--cadence", "CONTINUOUS"}, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
	var v health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal: %v; stdout = %s", err, stdout.String())
	}
	prolog := catalogtest.CheckIDsAtCadence(catalog.New(), health.Prolog)
	if len(prolog) == 0 {
		t.Fatal("builtin catalog has no Prolog checks")
	}
	for _, id := range prolog {
		var reason health.SkipReason
		for _, skipped := range v.SkippedChecks {
			if skipped.CheckID == id {
				reason = skipped.SkipReason
				break
			}
		}
		if reason != health.CadenceMismatch {
			t.Errorf("%s skip reason = %v, want CADENCE_MISMATCH", id, reason)
		}
	}
	if !verdictHasCheck(v, "gpu_count") {
		t.Error("CONTINUOUS run missing Continuous check gpu_count")
	}
}
