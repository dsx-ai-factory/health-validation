// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package celeval

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	vars := map[string]any{"temperature": nil, "limit": nil, "samples": nil}
	const comparison = "temperature > limit"
	if err := Validate([]string{comparison, "samples.exists(sample, sample > limit)"}, vars); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for _, tt := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "undeclared name", expression: "temperature > missing", want: "undeclared reference to 'missing'"},
		{name: "syntax error", expression: "temperature >", want: "Syntax error"},
		{name: "arithmetic result", expression: "temperature + 1", want: "has type int, want bool"},
		{name: "size result", expression: "size(samples)", want: "has type int, want bool"},
		{name: "string result", expression: "'x'", want: "has type string, want bool"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate([]string{comparison, tt.expression}, vars)
			if err == nil || !strings.Contains(err.Error(), tt.expression) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate = %v, want expression %q and %q", err, tt.expression, tt.want)
			}
		})
	}
}

func TestValidateAllowsBooleanResults(t *testing.T) {
	vars := map[string]any{"slowdown": true}
	for _, tt := range []struct {
		name       string
		expression string
	}{
		{name: "dynamic", expression: "slowdown"},
		{name: "protobuf bool wrapper", expression: "google.protobuf.BoolValue{value: true}"},
		{
			name:       "protobuf any containing bool",
			expression: `google.protobuf.Any{type_url: "type.googleapis.com/google.protobuf.BoolValue", value: b"\x08\x01"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate([]string{tt.expression}, vars); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got, err := New().Eval(tt.expression, vars); err != nil || !got {
				t.Errorf("Eval = %t, %v, want true without error", got, err)
			}
		})
	}
}

func TestEval_BasicComparison(t *testing.T) {
	e := New()

	got, err := e.Eval("temperature > max_temp", map[string]any{
		"temperature": 87.5,
		"max_temp":    85.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true, 87.5 > 85.0")
	}
}

func TestEval_NotMatched(t *testing.T) {
	e := New()

	got, err := e.Eval("errors > threshold", map[string]any{
		"errors":    int64(5),
		"threshold": int64(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false, 5 is not > 10")
	}
}

func TestEval_BooleanLogic(t *testing.T) {
	e := New()

	got, err := e.Eval("a > 0 && b > 0", map[string]any{
		"a": int64(1),
		"b": int64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestEval_CachesProgram(t *testing.T) {
	e := New()
	expr := "x > 0"
	vars := map[string]any{"x": int64(1)}

	// First call compiles.
	if _, err := e.Eval(expr, vars); err != nil {
		t.Fatal(err)
	}
	// Second call uses cache.
	if _, err := e.Eval(expr, vars); err != nil {
		t.Fatal(err)
	}

	e.mu.RLock()
	count := len(e.programs)
	e.mu.RUnlock()

	if count != 1 {
		t.Errorf("expected 1 cached program, got %d", count)
	}
}

func TestEval_CompileError(t *testing.T) {
	e := New()

	_, err := e.Eval("this is not valid CEL !!!", map[string]any{"x": 1})
	if err == nil {
		t.Error("expected compile error")
	}
}

func TestEval_NonBoolResult(t *testing.T) {
	const expression = "x"
	vars := map[string]any{"x": int64(1)}
	if err := Validate([]string{expression}, vars); err != nil {
		t.Fatalf("Validate dynamic expression: %v", err)
	}
	if _, err := New().Eval(expression, vars); err == nil || !strings.Contains(err.Error(), "produced int64, want bool") {
		t.Errorf("Eval error = %v, want a nonboolean result error", err)
	}
}
