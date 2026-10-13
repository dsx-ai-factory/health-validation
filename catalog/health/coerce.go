// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// coerceThresholdValue converts val to the Go type tt declares. Neither
// source lands there by itself — a Go literal's untyped constant becomes
// int, not int64, and YAML decodes integers as int, decimals as float64,
// and sequences as []any — and CEL comparisons are type-sensitive.
//
// An undeclared or unknown type is an error, never a pass-through: a value
// nothing can validate must not reach CEL.
func coerceThresholdValue(val any, tt ThresholdType) (any, error) {
	switch tt {
	case ThresholdInt64:
		switch v := val.(type) {
		case int:
			return int64(v), nil
		case int32:
			return int64(v), nil
		case int64:
			return v, nil
		case float64:
			// An infinity states intent — an unbounded tolerance — so
			// saturate. Converting instead yields the opposite extreme and
			// inverts the rule. A finite value out of range indicates
			// something went wrong.
			if math.IsInf(v, 0) {
				if v > 0 {
					return int64(math.MaxInt64), nil
				}
				return int64(math.MinInt64), nil
			}
			if v != math.Trunc(v) {
				return nil, fmt.Errorf("expected an integer, got %v", v)
			}
			if v < math.MinInt64 || v >= math.MaxInt64 {
				return nil, fmt.Errorf("integer %v is out of range for int64", v)
			}
			return int64(v), nil
		default:
			return nil, fmt.Errorf("expected an integer, got %T", val)
		}
	case ThresholdFloat64:
		switch v := val.(type) {
		case int:
			return exactFloat64(int64(v))
		case int32:
			return float64(v), nil
		case int64:
			return exactFloat64(v)
		case float64:
			// NaN compares false against everything, disabling a rule in
			// both directions; no threshold means that. An infinity does —
			// an unbounded tolerance — so it passes.
			if math.IsNaN(v) {
				return nil, fmt.Errorf("expected a number, got NaN")
			}
			return v, nil
		default:
			return nil, fmt.Errorf("expected a number, got %T", val)
		}
	case ThresholdBool:
		if v, ok := val.(bool); ok {
			return v, nil
		}
		return nil, fmt.Errorf("expected a boolean, got %T", val)
	case ThresholdString:
		if v, ok := val.(string); ok {
			return v, nil
		}
		return nil, fmt.Errorf("expected a string, got %T", val)
	case ThresholdStringList:
		return coerceStringList(val)
	case ThresholdTypeUnspecified:
		return nil, fmt.Errorf("threshold declares no value type")
	default:
		return nil, fmt.Errorf("unknown threshold value type %s", tt)
	}
}

// exactFloat64 converts v only when float64 holds it exactly. Above 2^53
// the representable integers thin out, and rounding would enforce a
// threshold the operator never wrote. The upper comparison keeps the
// round-trip itself in range, since float64(math.MaxInt64) rounds to 2^63.
func exactFloat64(v int64) (any, error) {
	f := float64(v)
	if f >= math.MaxInt64 || int64(f) != v {
		return nil, fmt.Errorf("integer %d cannot be represented exactly as a float64", v)
	}
	return f, nil
}

// coerceStringList accepts both shapes a string list arrives in: []any
// from YAML, which cannot produce a typed slice decoding into any, and
// []string from a Go literal.
func coerceStringList(val any) (any, error) {
	var items []string
	switch v := val.(type) {
	case []string:
		items = slices.Clone(v)
	case []any:
		items = make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected a list of strings, item %d is %T", i, item)
			}
			items[i] = s
		}
	default:
		return nil, fmt.Errorf("expected a list of strings, got %T", val)
	}

	for i := range items {
		items[i] = strings.TrimSpace(items[i])
		if items[i] == "" {
			return nil, fmt.Errorf("expected a list of non-empty strings, item %d is empty", i)
		}
	}
	return items, nil
}

// Normalize returns a copy of spec with both bounds, Value, and every
// PerArchitecture entry coerced to ValueType. Registration applies it to
// establish the invariant documented on [ThresholdSpec].
//
// PerArchitecture is rebuilt rather than written through: a spec copy
// still shares its caller's map backing store.
func (spec ThresholdSpec) Normalize() (ThresholdSpec, error) {
	// Bounds first, since CoerceInput compares against them.
	spec, err := spec.coerceBounds()
	if err != nil {
		return spec, err
	}

	if spec.Value != nil {
		coerced, err := spec.CoerceInput(spec.Value)
		if err != nil {
			return spec, err
		}
		spec.Value = coerced
	}

	if len(spec.PerArchitecture) > 0 {
		perArch := make(map[string]any, len(spec.PerArchitecture))
		for arch, value := range spec.PerArchitecture {
			coerced, err := spec.CoerceInput(value)
			if err != nil {
				return spec, err
			}
			perArch[arch] = coerced
		}
		spec.PerArchitecture = perArch
	}

	return spec, nil
}

// CoerceInput converts val to the threshold's declared type and checks it
// against the declared bounds. It is the single entry point for both
// catalog-authored values and operator overrides.
//
// Only val is treated as untyped; the bounds are expected to already hold
// ValueType's Go type, which registration guarantees — see
// [ThresholdSpec]. On an unregistered spec with bare-literal bounds this
// reports an ordering error rather than comparing mixed types.
func (spec ThresholdSpec) CoerceInput(val any) (any, error) {
	coerced, err := coerceThresholdValue(val, spec.ValueType)
	if err != nil {
		return nil, err
	}
	if err := checkBounds(coerced, spec.ValueMin, spec.ValueMax); err != nil {
		return nil, err
	}
	return coerced, nil
}

// checkBounds verifies an already-coerced value against already-coerced
// inclusive bounds. Either bound may be nil.
func checkBounds(coerced, minimum, maximum any) error {
	if minimum != nil {
		order, err := compareThresholdValues(coerced, minimum)
		if err != nil {
			return fmt.Errorf("value_min: %w", err)
		}
		if order < 0 {
			return fmt.Errorf("value %v is below the minimum %v", coerced, minimum)
		}
	}

	if maximum != nil {
		order, err := compareThresholdValues(coerced, maximum)
		if err != nil {
			return fmt.Errorf("value_max: %w", err)
		}
		if order > 0 {
			return fmt.Errorf("value %v is above the maximum %v", coerced, maximum)
		}
	}

	return nil
}

// coerceBounds returns a copy of spec with ValueMin and ValueMax coerced
// to ValueType, which [ThresholdSpec.CoerceInput] expects before it can
// compare against them.
func (spec ThresholdSpec) coerceBounds() (ThresholdSpec, error) {
	for _, bound := range []*any{&spec.ValueMin, &spec.ValueMax} {
		if *bound == nil {
			continue
		}
		coerced, err := coerceThresholdValue(*bound, spec.ValueType)
		if err != nil {
			return spec, err
		}
		*bound = coerced
	}
	return spec, nil
}

// compareThresholdValues orders two coerced values of the same threshold
// type. Comparing int64 directly rather than via float64 keeps values
// above 2^53 exact; a type mismatch means one side was never coerced.
func compareThresholdValues(a, b any) (int, error) {
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		if !ok {
			return 0, fmt.Errorf("want int64 to match the value, got %T: bound was not coerced to the declared type", b)
		}
		return cmp.Compare(av, bv), nil
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return 0, fmt.Errorf("want float64 to match the value, got %T: bound was not coerced to the declared type", b)
		}
		return cmp.Compare(av, bv), nil
	default:
		return 0, fmt.Errorf("type %T does not support ordering", a)
	}
}

// ThresholdValueError is returned when a threshold value cannot be
// coerced to its declared type or falls outside its declared bounds.
type ThresholdValueError struct {
	Err           error
	ThresholdName string
	CheckID       string
}

func (e *ThresholdValueError) Error() string {
	return fmt.Sprintf("threshold %q on check %q: %v", e.ThresholdName, e.CheckID, e.Err)
}

func (e *ThresholdValueError) Unwrap() error { return e.Err }
