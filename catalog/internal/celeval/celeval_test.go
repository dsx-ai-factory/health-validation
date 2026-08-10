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
