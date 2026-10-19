// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health_test

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Catalog authors write Go literals and YAML supplies int/float64, so
// both have to land on the declared type.
func TestCoerceInput(t *testing.T) {
	tests := []struct {
		value     any
		want      any
		name      string
		valueType health.ThresholdType
	}{
		{name: "bare int literal to int64", value: 1200, valueType: health.ThresholdInt64, want: int64(1200)},
		{name: "int64 to int64", value: int64(1200), valueType: health.ThresholdInt64, want: int64(1200)},
		{name: "integral float to int64", value: float64(1200), valueType: health.ThresholdInt64, want: int64(1200)},
		{name: "bare int literal to float64", value: 1200, valueType: health.ThresholdFloat64, want: float64(1200)},
		{name: "int64 to float64", value: int64(1200), valueType: health.ThresholdFloat64, want: float64(1200)},
		{name: "float64 to float64", value: 1200.5, valueType: health.ThresholdFloat64, want: 1200.5},
		{name: "largest exactly-representable integer to float64", value: int64(1 << 53), valueType: health.ThresholdFloat64, want: float64(1 << 53)},
		{name: "bool", value: true, valueType: health.ThresholdBool, want: true},
		{name: "string", value: "P0", valueType: health.ThresholdString, want: "P0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := health.ThresholdSpec{ValueType: tt.valueType}
			got, err := spec.CoerceInput(tt.value)
			if err != nil {
				t.Fatalf("CoerceInput: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCoerceInputRejects(t *testing.T) {
	tests := []struct {
		value     any
		name      string
		wantErr   string
		valueType health.ThresholdType
	}{
		// The regression that motivated declaring the type: a string
		// where a number belongs used to pass through unchecked.
		{name: "string to float64", value: "not-a-number", valueType: health.ThresholdFloat64, wantErr: "expected a number, got string"},
		{name: "string to int64", value: "four", valueType: health.ThresholdInt64, wantErr: "expected an integer, got string"},
		{name: "fractional float to int64", value: 3.5, valueType: health.ThresholdInt64, wantErr: "expected an integer, got 3.5"},
		// float64 cannot hold an odd integer above 2^53, and rounding it
		// would enforce a threshold the operator never wrote.
		{name: "lossy integer to float64", value: int64(1<<53 + 1), valueType: health.ThresholdFloat64, wantErr: "cannot be represented exactly as a float64"},
		{name: "largest int64 to float64", value: int64(math.MaxInt64), valueType: health.ThresholdFloat64, wantErr: "cannot be represented exactly as a float64"},
		{name: "int to bool", value: 1, valueType: health.ThresholdBool, wantErr: "expected a boolean, got int"},
		{name: "int to string", value: 1, valueType: health.ThresholdString, wantErr: "expected a string, got int"},
		// An undeclared type must fail rather than pass the value along.
		{name: "unspecified type", value: 1, valueType: health.ThresholdTypeUnspecified, wantErr: "declares no value type"},
		{name: "unknown type", value: 1, valueType: health.ThresholdType(99), wantErr: "unknown threshold value type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := health.ThresholdSpec{ValueType: tt.valueType}
			if _, err := spec.CoerceInput(tt.value); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// YAML resolves .inf and .nan to float64, so both reach coercion. Neither
// is a usable threshold, and a resolved one breaks verdict encoding, so
// both are rejected — an inapplicable check is disabled, not widened.
func TestCoerceInputNonFinite(t *testing.T) {
	ints := health.ThresholdSpec{ValueType: health.ThresholdInt64}
	floats := health.ThresholdSpec{ValueType: health.ThresholdFloat64}

	for _, tt := range []struct {
		value   any
		wantErr string
		spec    health.ThresholdSpec
	}{
		{spec: floats, value: math.Inf(1), wantErr: "expected a finite number, got +Inf"},
		{spec: floats, value: math.Inf(-1), wantErr: "expected a finite number, got -Inf"},
		{spec: floats, value: math.NaN(), wantErr: "expected a finite number, got NaN"},
		{spec: ints, value: math.Inf(1), wantErr: "expected a finite integer, got +Inf"},
		{spec: ints, value: math.Inf(-1), wantErr: "expected a finite integer, got -Inf"},
		{spec: ints, value: math.NaN(), wantErr: "expected a finite integer, got NaN"},
		{spec: ints, value: 1e300, wantErr: "out of range for int64"},
	} {
		_, err := tt.spec.CoerceInput(tt.value)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("CoerceInput(%v) as %s error = %v, want %q",
				tt.value, tt.spec.ValueType, err, tt.wantErr)
		}
	}
}

// The reason non-finite values are rejected at all: a resolved threshold
// lands in every verdict, including a passing one, and encoding/json fails
// the whole node verdict rather than just that check's.
func TestVerdictEncodingRejectsNonFinite(t *testing.T) {
	v := health.NodeVerdict{
		CheckResults: []health.CheckVerdict{{
			CheckID:  "probe",
			Severity: health.Pass,
			Thresholds: map[string]health.ResolvedThreshold{
				"max_power_w": {Value: math.Inf(1), OriginalValue: float64(1500), Overridden: true},
			},
		}},
	}
	if _, err := json.Marshal(v); err == nil {
		t.Fatal("marshaling a verdict with an infinite threshold succeeded, want an error")
	}
}

// A string list arrives as []any from YAML and as []string from a Go
// literal. Both are valid, and both reach CEL with their items verbatim —
// stripping whitespace is the config decoder's job, not coercion's.
func TestCoerceStringList(t *testing.T) {
	spec := health.ThresholdSpec{ValueType: health.ThresholdStringList}
	want := []string{" mlx5_0 ", "mlx5_1"}

	for _, value := range []any{
		[]any{" mlx5_0 ", "mlx5_1"},
		[]string{" mlx5_0 ", "mlx5_1"},
	} {
		got, err := spec.CoerceInput(value)
		if err != nil {
			t.Fatalf("CoerceInput(%T): %v", value, err)
		}
		if !slices.Equal(got.([]string), want) {
			t.Errorf("%T: got %#v, want %#v", value, got, want)
		}
	}
}

// Coercing a []string must not hand back the caller's backing array, or a
// registered threshold would alias whatever the author can still write to.
func TestCoerceStringListCopies(t *testing.T) {
	spec := health.ThresholdSpec{ValueType: health.ThresholdStringList}
	original := []string{"mlx5_0"}
	got, err := spec.CoerceInput(original)
	if err != nil {
		t.Fatalf("CoerceInput: %v", err)
	}
	original[0] = "mlx5_1"
	if coerced := got.([]string); coerced[0] != "mlx5_0" {
		t.Errorf("coerced slice aliases caller: got %q", coerced[0])
	}
}

func TestCoerceRejectsInvalidStringList(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantErr string
	}{
		{name: "not a list", value: "mlx5_0", wantErr: "expected a list of strings"},
		{name: "non-string item", value: []any{"mlx5_0", 1}, wantErr: "item 1 is int"},
		{name: "empty item", value: []any{"mlx5_0", ""}, wantErr: "item 1 is empty"},
		{name: "empty item in typed slice", value: []string{"mlx5_0", ""}, wantErr: "item 1 is empty"},
	}

	spec := health.ThresholdSpec{ValueType: health.ThresholdStringList}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := spec.CoerceInput(tt.value); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// Bounds are compared as-is, so they carry the declared type the way a
// registered spec does.
func TestCoerceInputChecksBounds(t *testing.T) {
	spec := health.ThresholdSpec{
		ValueType: health.ThresholdInt64,
		ValueMin:  int64(0),
		ValueMax:  int64(15),
		Category:  health.DefaultThreshold,
	}

	for _, value := range []any{0, 7, 15} {
		if _, err := spec.CoerceInput(value); err != nil {
			t.Errorf("CoerceInput(%v) = %v, want in bounds", value, err)
		}
	}

	for _, tc := range []struct {
		value   any
		wantErr string
	}{
		{value: -1, wantErr: "below the minimum 0"},
		{value: 16, wantErr: "above the maximum 15"},
	} {
		_, err := spec.CoerceInput(tc.value)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("CoerceInput(%v) error = %v, want %q", tc.value, err, tc.wantErr)
		}
	}
}

// Bounds are independently optional: a tolerance threshold bounds only
// its lower end so operators can still disable it by setting it high.
func TestCoerceInputOneSidedBounds(t *testing.T) {
	spec := health.ThresholdSpec{ValueType: health.ThresholdInt64, ValueMin: int64(0)}

	if _, err := spec.CoerceInput(1_000_000); err != nil {
		t.Errorf("CoerceInput with no ValueMax = %v, want no error", err)
	}
	if _, err := spec.CoerceInput(-1); err == nil {
		t.Error("CoerceInput(-1) = nil, want below-minimum error")
	}
}

// Coercion belongs at the two boundaries that take untyped input, so
// CoerceInput does not re-coerce the bounds. An unregistered spec whose
// bounds are bare literals has to say so rather than compare mixed types.
func TestCoerceInputRequiresCoercedBounds(t *testing.T) {
	spec := health.ThresholdSpec{ValueType: health.ThresholdInt64, ValueMin: 0}

	_, err := spec.CoerceInput(7)
	if err == nil || !strings.Contains(err.Error(), "not coerced to the declared type") {
		t.Fatalf("error = %v, want an un-coerced bound reported", err)
	}

	// Normalize is what registration applies to establish the invariant.
	normalized, err := spec.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if normalized.ValueMin != int64(0) {
		t.Errorf("ValueMin = %#v, want int64(0)", normalized.ValueMin)
	}
	if _, err := normalized.CoerceInput(7); err != nil {
		t.Errorf("CoerceInput after Normalize = %v, want no error", err)
	}
}
