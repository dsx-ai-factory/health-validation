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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestOverridesCoercesToThresholdType(t *testing.T) {
	cfg := &Config{Checks: map[string]map[string]any{
		"gpu_count": {"expected_gpu_count": 8},
	}}

	os, err := cfg.Overrides(catalog.New())
	if err != nil {
		t.Fatalf("Overrides: %v", err)
	}

	got := os.ForCheck("gpu_count")["expected_gpu_count"]
	want := int64(8)
	if got != want {
		t.Fatalf("expected_gpu_count = %#v (%T), want %#v (%T)", got, got, want, want)
	}
}

func TestOverridesRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		checks  map[string]map[string]any
		wantErr string
	}{
		{
			name:    "unknown check",
			checks:  map[string]map[string]any{"no_such_check": {"x": 1}},
			wantErr: `unknown check "no_such_check"`,
		},
		{
			name:    "unknown parameter",
			checks:  map[string]map[string]any{"gpu_count": {"bogus": 1}},
			wantErr: `check "gpu_count" has no parameter "bogus"`,
		},
		{
			name:    "non-coercible value",
			checks:  map[string]map[string]any{"gpu_count": {"expected_gpu_count": "four"}},
			wantErr: `expected an integer, got string`,
		},
		{
			name:    "fractional value for integer threshold",
			checks:  map[string]map[string]any{"gpu_count": {"expected_gpu_count": 3.5}},
			wantErr: `expected an integer, got 3.5`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Checks: tt.checks}
			_, err := cfg.Overrides(catalog.New())
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigParsesNestedChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "checks:\n  gpu_count:\n    expected_gpu_count: 4\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Checks["gpu_count"]["expected_gpu_count"]; got != 4 {
		t.Fatalf("expected_gpu_count = %#v, want 4", got)
	}
}

// TestLoadConfigRejectsUnknownKeys guards the KnownFields(true) decode: a
// typo like `check:` instead of `checks:` must error rather than silently
// yield an empty override set.
func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "check:\n  gpu_count:\n    expected_gpu_count: 4\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "check") {
		t.Fatalf("error = %q, want it to mention the unknown field", err.Error())
	}
}

// TestLoadConfigEmptyFile confirms an empty config is a valid "no
// overrides" input: Decode returns io.EOF, which LoadConfig tolerates.
func TestLoadConfigEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Checks) != 0 {
		t.Fatalf("Checks = %v, want empty", cfg.Checks)
	}
}

// TestEvaluatePassingNode exercises the orchestration path with mock
// backends: a healthy 4-GPU node should produce a passing verdict and
// exit code 0.
func TestEvaluatePassingNode(t *testing.T) {
	cat := catalog.New()
	sensors := []toolkit.Sensor{
		toolkit.NewGPUSensor(toolkit.MockGPU(
			gpu.GPUMeasurement{GPUID: "GPU-0"},
		)),
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 4},
		)),
	}

	var stdout, stderr bytes.Buffer
	code := evaluate(cat, toolkit.DefaultSensorTimeout, "go-health-validation", testNodeID, &stdout, &stderr, sensors...)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal stdout: %v\n%s", err, stdout.String())
	}
	if verdict.OverallSeverity != health.Pass {
		t.Fatalf("overall severity = %v, want PASS", verdict.OverallSeverity)
	}
	if verdict.NodeID != testNodeID {
		t.Fatalf("NodeID = %q, want %q", verdict.NodeID, testNodeID)
	}
}

// TestBuildCatalogAppliesConfigOverrides is the end-to-end guard: a
// config file's check parameters must actually reach evaluation, not just
// parse. Loads a config that lowers gpu_count's expected count and
// confirms the resolved threshold (and the resulting verdict) reflect it.
func TestBuildCatalogAppliesConfigOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "checks:\n  gpu_count:\n    expected_gpu_count: 8\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, _, err := buildCatalog(path, nil, "")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}

	// 8 visible GPUs: Critical at the default expected count of 4,
	// PASS once the threshold is raised to 8 from config.
	verdicts, err := cat.EvaluateAllRaw(node.NodeMeasurement{GPUCount: 8})
	if err != nil {
		t.Fatal(err)
	}

	var checked bool
	for _, v := range verdicts {
		if v.CheckID != "gpu_count" {
			continue
		}
		checked = true
		rt := v.Thresholds["expected_gpu_count"]
		if !rt.Overridden || rt.Value != int64(8) {
			t.Errorf("expected_gpu_count = %+v, want overridden value 8", rt)
		}
		if v.Severity != health.Pass {
			t.Errorf("severity = %v, want PASS with the raised threshold", v.Severity)
		}
	}
	if !checked {
		t.Fatal("gpu_count verdict not found")
	}
}

// TestEvaluateGPUCountMismatch confirms a wrong GPU count fails the
// gpu_count check Critical and the CLI propagates that into exit code 1:
// a produced CRITICAL verdict is an out-of-service outcome. The full
// severity still lives in the JSON verdict written to stdout.
func TestEvaluateGPUCountMismatch(t *testing.T) {
	cat := catalog.New()
	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 3},
		)),
	}

	var stdout, stderr bytes.Buffer
	code := evaluate(cat, toolkit.DefaultSensorTimeout, "go-health-validation", testNodeID, &stdout, &stderr, sensors...)
	if code != exitUnhealthy {
		t.Fatalf("exit code = %d, want %d (CRITICAL propagated); stderr=%q", code, exitUnhealthy, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal stdout: %v\n%s", err, stdout.String())
	}
	if verdict.OverallSeverity != health.Critical {
		t.Fatalf("overall severity = %v, want CRITICAL", verdict.OverallSeverity)
	}
}

// TestLoadConfig_CollectionTimeout covers parsing the optional global
// collection timeout and its default fallback when unset.
func TestLoadConfig_CollectionTimeout(t *testing.T) {
	set := filepath.Join(t.TempDir(), "set.yaml")
	if err := os.WriteFile(set, []byte("collection_timeout_seconds: 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(set)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CollectionTimeoutSeconds != 30 {
		t.Errorf("CollectionTimeoutSeconds = %d, want 30", cfg.CollectionTimeoutSeconds)
	}
	if got := cfg.CollectionTimeout(); got != 30*time.Second {
		t.Errorf("CollectionTimeout() = %v, want 30s", got)
	}

	// Unset: the resolved timeout falls back to the toolkit default.
	unset := filepath.Join(t.TempDir(), "unset.yaml")
	if err = os.WriteFile(unset, []byte("checks: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(unset)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.CollectionTimeout(); got != toolkit.DefaultSensorTimeout {
		t.Errorf("CollectionTimeout() = %v, want default %v", got, toolkit.DefaultSensorTimeout)
	}
}

// TestLoadConfig_RejectsNegativeTimeout guards the load-time validation.
func TestLoadConfig_RejectsNegativeTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("collection_timeout_seconds: -5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("expected error for negative collection_timeout_seconds")
	}
}

// TestLoadConfig_RejectsOverflowTimeout guards the upper bound: a value
// large enough to overflow time.Duration when scaled to seconds (which
// would wrap negative and expire immediately) is rejected at load.
func TestLoadConfig_RejectsOverflowTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overflow.yaml")
	if err := os.WriteFile(path, []byte("collection_timeout_seconds: 10000000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("expected error for collection_timeout_seconds that overflows time.Duration")
	}
}

// TestBuildCatalogResolvesTimeout is the end-to-end guard that a
// configured timeout reaches the run path, and that the toolkit default
// applies with no config.
func TestBuildCatalogResolvesTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("collection_timeout_seconds: 45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, timeout, err := buildCatalog(path, nil, "")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	if timeout != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", timeout)
	}

	_, timeout, err = buildCatalog("", nil, "")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	if timeout != toolkit.DefaultSensorTimeout {
		t.Errorf("timeout = %v, want default %v", timeout, toolkit.DefaultSensorTimeout)
	}
}
