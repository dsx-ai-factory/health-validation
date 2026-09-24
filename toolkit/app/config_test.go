package app

import (
	"bytes"
	"encoding/json"
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

// coerce handles []string thresholds (whitespace-trimmed, rejecting
// non-lists, non-string items, and empties). Exercised directly on the
// coercion helper since no built-in check currently declares a []string
// threshold to route it through Overrides.
func TestCoerceStringList(t *testing.T) {
	got, err := coerce([]any{" mlx5_0 ", "mlx5_1"}, []string{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	want := []string{"mlx5_0", "mlx5_1"}
	if !slices.Equal(got.([]string), want) {
		t.Fatalf("string list = %#v, want %#v", got, want)
	}
}

func TestCoerceRejectsInvalidStringList(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantErr string
	}{
		{name: "not a list", value: "mlx5_0", wantErr: "expected a list of strings"},
		{name: "non-string", value: []any{"mlx5_0", 1}, wantErr: "item 1 is int"},
		{name: "empty", value: []any{"mlx5_0", " "}, wantErr: "item 1 is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := coerce(tt.value, []string{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
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
			// Regression for #85: gpu_power_limit's expected_power_limit_w
			// threshold has no flat Value, only PerArchitecture entries, so
			// coerce must still validate against their type instead of
			// letting ref==nil pass anything through unchecked.
			name:    "non-coercible value for PerArchitecture-only threshold",
			checks:  map[string]map[string]any{"gpu_power_limit": {"expected_power_limit_w": "not-a-number"}},
			wantErr: `expected a number, got string`,
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
	if want := []string{"gpu_count"}; !slices.Equal(got, want) {
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
	code := evaluate(cat, toolkit.DefaultSensorTimeout, "health-validation", testNodeID, &stdout, &stderr, sensors...)
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

	cat, _, err := buildCatalog(path, nil, "GB200")
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

	cat, timeout, err := buildCatalog(path, nil, "GB200")
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}

	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(toolkit.MockNode(
			node.NodeMeasurement{GPUCount: 4},
		)),
	}

	var stdout, stderr bytes.Buffer
	code := evaluate(cat, timeout, "health-validation", testNodeID, &stdout, &stderr, sensors...)
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
	code := evaluate(cat, toolkit.DefaultSensorTimeout, "health-validation", testNodeID, &stdout, &stderr, sensors...)
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
