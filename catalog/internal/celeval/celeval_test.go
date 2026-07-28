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

package celeval

import "testing"

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
	e := New()

	_, err := e.Eval("x + 1", map[string]any{"x": int64(1)})
	if err == nil {
		t.Error("expected error for non-bool result")
	}
}
