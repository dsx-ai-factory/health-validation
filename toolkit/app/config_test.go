// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// mustLoadConfig loads a config file the test just wrote, failing the test if
// it does not parse. buildCatalog takes a parsed config, so the run path loads
// the file once and shares it with metrics publishing.
func mustLoadConfig(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", path, err)
	}
	return cfg
}

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

func TestOverridesCoercesAveragePowerDrawBounds(t *testing.T) {
	cfg := &Config{Checks: map[string]map[string]any{
		"gpu_average_power_draw": {
			"min_power_w": 65,
			"max_power_w": 1500,
		},
	}}

	os, err := cfg.Overrides(catalog.New())
	if err != nil {
		t.Fatalf("Overrides: %v", err)
	}

	got := os.ForCheck("gpu_average_power_draw")
	if got["min_power_w"] != float64(65) || got["max_power_w"] != float64(1500) {
		t.Fatalf("average power bounds = %#v, want min=65 and max=1500 as float64", got)
	}
}

// stringListCatalog holds one check whose only threshold is a string list,
// a shape no built-in check uses yet.
func stringListCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty()
	err := cat.Register(health.CheckDefinition{
		ID:              "device_inventory",
		Name:            "Device inventory",
		Category:        health.Node,
		RequiredSignals: []string{"gpu_count"},
		Thresholds: map[string]health.ThresholdSpec{
			"expected_devices": {
				Value:       []string{"mlx5_0"},
				ValueType:   health.ThresholdStringList,
				Category:    health.DefaultThreshold,
				Description: "Arbitrary test threshold.",
			},
		},
		Rules: []health.Rule{{Expression: "gpu_count < 0", Severity: health.Critical, Message: "unused"}},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return cat
}

// A YAML list item can pick up whitespace the operator never meant, so the
// config trims it. Coercion does not, which is why this lives here.
func TestOverridesTrimsStringListItems(t *testing.T) {
	cfg := &Config{Checks: map[string]map[string]any{
		"device_inventory": {"expected_devices": []any{" mlx5_0 ", "mlx5_1"}},
	}}

	os, err := cfg.Overrides(stringListCatalog(t))
	if err != nil {
		t.Fatalf("Overrides: %v", err)
	}

	got, ok := os.ForCheck("device_inventory")["expected_devices"].([]string)
	if !ok {
		t.Fatalf("expected_devices = %#v, want []string", os.ForCheck("device_inventory")["expected_devices"])
	}
	if want := []string{"mlx5_0", "mlx5_1"}; !slices.Equal(got, want) {
		t.Fatalf("expected_devices = %#v, want %#v", got, want)
	}
}

// An all-whitespace item has to stay an error: trimming leaves it empty and
// coercion rejects it, so the two halves still compose.
func TestOverridesRejectsWhitespaceStringListItem(t *testing.T) {
	cfg := &Config{Checks: map[string]map[string]any{
		"device_inventory": {"expected_devices": []any{"mlx5_0", "  "}},
	}}

	_, err := cfg.Overrides(stringListCatalog(t))
	if err == nil || !strings.Contains(err.Error(), "item 1 is empty") {
		t.Fatalf("error = %v, want one naming an empty item", err)
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
		{
			// Regression for #85: expected_power_limit_w carries no flat
			// Value, only PerArchitecture entries, so its type comes from
			// ValueType rather than from a default to compare against.
			name:    "non-coercible value for a per-architecture-only threshold",
			checks:  map[string]map[string]any{"gpu_power_limit": {"expected_power_limit_w": "not-a-number"}},
			wantErr: `expected a number, got string`,
		},
		{
			name:    "below minimum",
			checks:  map[string]map[string]any{"gpu_count": {"expected_gpu_count": 0}},
			wantErr: `below the minimum 1`,
		},
		{
			name:    "above maximum",
			checks:  map[string]map[string]any{"gpu_power_state": {"expected_gpu_pstate": 16}},
			wantErr: `above the maximum 15`,
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

// TestLoadConfigParsesDisabledChecks confirms disabled_checks decodes
// independently of checks: a config may both tune and disable.
func TestLoadConfigParsesDisabledChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "checks:\n  gpu_count:\n    expected_gpu_count: 4\ndisabled_checks:\n  - gpu_power_state\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if want := []string{"gpu_power_state"}; !slices.Equal(cfg.DisabledChecks, want) {
		t.Errorf("DisabledChecks = %#v, want %#v", cfg.DisabledChecks, want)
	}
	if got := cfg.Checks["gpu_count"]["expected_gpu_count"]; got != 4 {
		t.Errorf("expected_gpu_count = %#v, want 4", got)
	}
}

// TestDisabledCheckIDsTrims guards the trim: a stray space in a YAML list
// item must not turn a valid ID into an unknown check, and the trimmed
// value — not the original — is what reaches the catalog, where an
// untrimmed key would silently miss the disabled-checks lookup.
func TestDisabledCheckIDsTrims(t *testing.T) {
	cfg := &Config{DisabledChecks: []string{"  gpu_count  "}}

	got, err := cfg.DisabledCheckIDs(catalog.New())
	if err != nil {
		t.Fatalf("DisabledCheckIDs: %v", err)
	}
	if want := []string{"gpu_count", "rdma_topo"}; !slices.Equal(got, want) {
		t.Fatalf("DisabledCheckIDs = %#v, want %#v", got, want)
	}
}

// TestDisabledCheckIDsRejectsBadConfig covers the validation that makes a
// config-file disable safe: a typo'd ID must be an error rather than a
// check that silently keeps running.
func TestDisabledCheckIDsRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name     string
		wantErr  string
		disabled []string
	}{
		{
			name:     "unknown check",
			wantErr:  `unknown check "no_such_check"`,
			disabled: []string{"no_such_check"},
		},
		{
			name:     "empty entry",
			wantErr:  "disabled_checks contains empty entry",
			disabled: []string{""},
		},
		{
			name:     "whitespace-only entry",
			wantErr:  "disabled_checks contains empty entry",
			disabled: []string{"   "},
		},
		{
			name:     "valid entry alongside an unknown one",
			wantErr:  `unknown check "no_such_check"`,
			disabled: []string{"gpu_count", "no_such_check"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{DisabledChecks: tt.disabled}
			_, err := cfg.DisabledCheckIDs(catalog.New())
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
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

// TestLoadConfigRejectsDisabledChecksTypo is the same guard for the
// disable key. The singular `disabled_check:` is the likely typo, and it is
// the dangerous direction: silently disabling nothing reads as a working
// config while every check the operator meant to switch off keeps running.
func TestLoadConfigRejectsDisabledChecksTypo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("disabled_check:\n  - gpu_count\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "disabled_check") {
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
// exit code 0. The architecture is set as the CLI does, so
// architecture-scoped thresholds resolve to real expectations.
func TestEvaluatePassingNode(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	sensors := []toolkit.Sensor{
		toolkit.NewGPUSensor(toolkit.MockGPU(
			gpu.GPUMeasurement{
				GPUID:               "GPU-0",
				AveragePowerDrawW:   220,
				PowerLimitW:         1200,
				EnforcedPowerLimitW: 1200,
			},
		)),
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 4},
		)),
	}

	var stdout, stderr bytes.Buffer
	_, _, code := evaluate(cat, toolkit.CollectionOptions{}, "health-validation", testNodeID, &stdout, &stderr, sensors...)
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

	cat, _, err := buildCatalog(mustLoadConfig(t, path), nil, "GB200", health.OperatorInfra)
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}

	// 8 visible GPUs: Critical at the default expected count of 4,
	// PASS once the threshold is raised to 8 from config.
	nv, err := cat.EvaluateAll(node.NodeMeasurement{GPUCount: 8})
	if err != nil {
		t.Fatal(err)
	}

	var checked bool
	for _, v := range nv.CheckResults {
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

// TestBuildCatalogDisablesChecks is the end-to-end guard for
// disabled_checks: the IDs must reach catalog construction, not merely
// parse and validate. The config raises gpu_count's expected count to 8
// and disables the check, so the 4-GPU node under test would be CRITICAL
// (exit 1) if the disable never arrived. The disabled check must land in
// skipped_checks with CHECK_DISABLED and stay out of overall_severity.
func TestBuildCatalogDisablesChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "checks:\n  gpu_count:\n    expected_gpu_count: 8\ndisabled_checks:\n  - gpu_count\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, timeout, err := buildCatalog(mustLoadConfig(t, path), nil, "GB200", health.OperatorInfra)
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}

	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 4},
		)),
	}

	var stdout, stderr bytes.Buffer
	_, _, code := evaluate(cat, toolkit.CollectionOptions{Timeout: timeout}, "health-validation", testNodeID, &stdout, &stderr, sensors...)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (a disabled check must not reach the verdict); stderr=%q",
			code, exitOK, stderr.String())
	}

	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal stdout: %v\n%s", err, stdout.String())
	}

	for _, v := range verdict.CheckResults {
		if v.CheckID == "gpu_count" {
			t.Fatalf("gpu_count produced a result despite being disabled: %+v", v)
		}
	}

	var skipped bool
	for _, v := range verdict.SkippedChecks {
		if v.CheckID != "gpu_count" {
			continue
		}
		skipped = true
		if v.SkipReason != health.CheckDisabled {
			t.Errorf("skip reason = %v, want CHECK_DISABLED", v.SkipReason)
		}
	}
	if !skipped {
		t.Error("gpu_count not found in skipped_checks")
	}

	if verdict.OverallSeverity != health.Pass {
		t.Errorf("overall severity = %v, want PASS (skips are excluded)", verdict.OverallSeverity)
	}
}

// TestEvaluateGPUCountMismatch confirms a wrong GPU count fails the
// gpu_count check Critical and the CLI propagates that into exit code 1:
// a produced CRITICAL verdict is an out-of-service outcome. The full
// severity still lives in the JSON verdict written to stdout.
func TestEvaluateGPUCountMismatch(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 3},
		)),
	}

	var stdout, stderr bytes.Buffer
	_, _, code := evaluate(cat, toolkit.CollectionOptions{}, "health-validation", testNodeID, &stdout, &stderr, sensors...)
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
	_, timeout, err := buildCatalog(mustLoadConfig(t, path), nil, "", health.OperatorInfra)
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	if timeout != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", timeout)
	}

	_, timeout, err = buildCatalog(nil, nil, "", health.OperatorInfra)
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	if timeout != toolkit.DefaultSensorTimeout {
		t.Errorf("timeout = %v, want default %v", timeout, toolkit.DefaultSensorTimeout)
	}
}

// TestLoadConfigParsesMetricsBlock is the round-trip guard for the metrics
// settings: every key an operator can write must reach the struct, or the
// endpoint silently stays unset and nothing is ever published.
func TestLoadConfigParsesMetricsBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `metrics:
  enabled: true
  endpoint: http://127.0.0.1:44318
  timeout_seconds: 30
  headers:
    x-api-key: secret
  resource_attributes:
    cluster: dsx-east
`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	m := cfg.Metrics
	if !m.Enabled {
		t.Error("metrics.enabled = false, want true")
	}
	if m.Endpoint != "http://127.0.0.1:44318" {
		t.Errorf("metrics.endpoint = %q", m.Endpoint)
	}
	if got := m.Timeout(); got != 30*time.Second {
		t.Errorf("Timeout() = %v, want 30s", got)
	}
	if m.Headers["x-api-key"] != "secret" {
		t.Errorf("metrics.headers = %v", m.Headers)
	}
	if m.ResourceAttributes["cluster"] != "dsx-east" {
		t.Errorf("metrics.resource_attributes = %v", m.ResourceAttributes)
	}
}

// TestMetricsTimeoutDefaults covers the unset case: an operator who enables
// metrics without a timeout still gets a bounded export.
func TestMetricsTimeoutDefaults(t *testing.T) {
	var m MetricsConfig
	if got := m.Timeout(); got != DefaultMetricsTimeout {
		t.Errorf("Timeout() = %v, want %v", got, DefaultMetricsTimeout)
	}
}

// TestLoadConfigRejectsBadMetrics checks the settings at load time rather than
// at publish time, when the run has already finished and the error is too late
// to act on. The bare host:port case matters most: it is the form the
// collector's own receiver config uses, so operators will copy it in.
func TestLoadConfigRejectsBadMetrics(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		want     string
	}{
		{
			name:     "enabled without endpoint",
			contents: "metrics:\n  enabled: true\n",
			want:     "metrics.endpoint is required",
		},
		{
			name:     "bare host and port",
			contents: "metrics:\n  enabled: true\n  endpoint: 127.0.0.1:44318\n",
			want:     "must be a full URL",
		},
		{
			name:     "unsupported scheme",
			contents: "metrics:\n  enabled: true\n  endpoint: grpc://127.0.0.1:44317\n",
			want:     "must be a full URL",
		},
		{
			name:     "no host",
			contents: "metrics:\n  enabled: true\n  endpoint: http://\n",
			want:     "has no host",
		},
		{
			name:     "negative timeout",
			contents: "metrics:\n  enabled: true\n  endpoint: http://h:1\n  timeout_seconds: -1\n",
			want:     "metrics.timeout_seconds must be between",
		},
		{
			name:     "unknown key",
			contents: "metrics:\n  enabled: true\n  endpoint: http://h:1\n  protocol: grpc\n",
			want:     "protocol",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(c.contents), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("LoadConfig: got nil error, want one mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

// TestDisabledMetricsSkipValidation lets an operator park a half-written
// endpoint under `enabled: false` without failing every run.
func TestDisabledMetricsSkipValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "metrics:\n  enabled: false\n  endpoint: 127.0.0.1:44318\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Errorf("LoadConfig: %v, want a disabled block to skip validation", err)
	}
}

// closedCollectorURL returns an already-shut-down httptest server's URL. A
// local closed port beats a literal address: guaranteed dead, and independent
// of how the environment routes a reserved range.
func closedCollectorURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

// mustNotHang is a hang detector, not a latency assertion: the budget sits far
// above the configured timeout.
func mustNotHang(t *testing.T, cfg MetricsConfig) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		publishMetrics(cfg, health.NodeVerdict{OverallSeverity: health.Pass}, "node-1")
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("publishMetrics did not return; a bad collector must not hang the run")
	}
}

// TestPublishMetricsSurvivesUnreachableCollector is the load-bearing guarantee:
// an unreachable collector changes neither the verdict nor the exit code.
func TestPublishMetricsSurvivesUnreachableCollector(t *testing.T) {
	mustNotHang(t, MetricsConfig{
		Enabled:        true,
		Endpoint:       closedCollectorURL(t),
		TimeoutSeconds: 1,
	})
}

// TestPublishMetricsSurvivesRejectingCollector covers the other failure shape:
// reachable, but answering with an error.
func TestPublishMetricsSurvivesRejectingCollector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mustNotHang(t, MetricsConfig{Enabled: true, Endpoint: srv.URL, TimeoutSeconds: 1})
}

// TestPublishMetricsDisabledIsNoop guards the default: an operator who never
// configures metrics must not pay for a network attempt.
func TestPublishMetricsDisabledIsNoop(t *testing.T) {
	publishMetrics(MetricsConfig{Endpoint: closedCollectorURL(t)},
		health.NodeVerdict{OverallSeverity: health.Pass}, "node-1")
}

// TestValidateStoresTrimmedEndpoint guards validating one value and publishing
// another: a quoted endpoint would pass validation and fail at publish time.
func TestValidateStoresTrimmedEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "metrics:\n  enabled: true\n  endpoint: \"  http://127.0.0.1:44318  \"\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Metrics.Endpoint; got != "http://127.0.0.1:44318" {
		t.Errorf("endpoint = %q, want it stored trimmed", got)
	}
}

// TestRedactEndpoint keeps credentials out of the captured stderr log while
// leaving enough to diagnose with.
func TestRedactEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "userinfo removed",
			endpoint: "https://user:hunter2@collector.example:4318/v1/metrics",
			want:     "https://redacted@collector.example:4318/v1/metrics",
		},
		{
			name:     "query dropped",
			endpoint: "https://collector.example/v1/metrics?api_key=secret",
			want:     "https://collector.example/v1/metrics",
		},
		{
			name:     "plain endpoint kept intact",
			endpoint: "http://127.0.0.1:44318/v1/metrics",
			want:     "http://127.0.0.1:44318/v1/metrics",
		},
		{
			name:     "unparseable is not echoed",
			endpoint: "127.0.0.1:44318",
			want:     "[unparseable]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactEndpoint(tt.endpoint)
			if got != tt.want {
				t.Errorf("redactEndpoint(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
			if strings.Contains(got, "hunter2") || strings.Contains(got, "secret") {
				t.Errorf("redactEndpoint(%q) = %q, which still carries a credential", tt.endpoint, got)
			}
		})
	}
}
