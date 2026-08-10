// Package celeval provides CEL expression evaluation for health check rules.
//
// It wraps cel-go with lazy compilation and caching: expressions are compiled
// on first evaluation when the full set of variable names is known, and
// compiled programs are reused for subsequent evaluations with the same
// variable set.
package celeval

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
)

// Evaluator compiles and caches CEL programs for health check rule
// evaluation. It is safe for concurrent use.
type Evaluator struct {
	programs map[string]cel.Program
	mu       sync.RWMutex
}

// New creates an Evaluator with an empty program cache.
func New() *Evaluator {
	return &Evaluator{
		programs: make(map[string]cel.Program),
	}
}

// Eval evaluates a CEL expression against the provided variables and
// returns whether it matched. Programs are compiled on first use and
// cached by (expression, variable names) for reuse.
//
// All variables are declared as dynamic types. The CEL runtime handles
// type coercion. The expression must evaluate to a bool.
func (e *Evaluator) Eval(expression string, vars map[string]any) (bool, error) {
	key := cacheKey(expression, vars)

	e.mu.RLock()
	prg, ok := e.programs[key]
	e.mu.RUnlock()

	if !ok {
		compiled, err := compile(expression, vars)
		if err != nil {
			return false, err
		}
		e.mu.Lock()
		// Double-check after acquiring write lock.
		if existing, exists := e.programs[key]; exists {
			prg = existing
		} else {
			e.programs[key] = compiled
			prg = compiled
		}
		e.mu.Unlock()
	}

	out, _, err := prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("evaluating %q: %w", expression, err)
	}

	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression %q produced %T, want bool", expression, out.Value())
	}
	return b, nil
}

func compile(expression string, vars map[string]any) (cel.Program, error) {
	opts := make([]cel.EnvOption, 0, len(vars))
	for name := range vars {
		opts = append(opts, cel.Variable(name, cel.DynType))
	}

	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	ast, iss := env.Compile(expression)
	if iss.Err() != nil {
		return nil, fmt.Errorf("compiling %q: %w", expression, iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("building program for %q: %w", expression, err)
	}

	return prg, nil
}

func cacheKey(expression string, vars map[string]any) string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	return expression + "\x00" + strings.Join(names, ",")
}
