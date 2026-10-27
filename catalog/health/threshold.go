// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import "fmt"

// ThresholdCategory classifies how a threshold value may be modified
// by operators. The catalog enforces governance rules: Fixed thresholds
// reject override attempts with an error; Default and Adjustable
// thresholds accept operator-supplied values.
type ThresholdCategory int

const (
	// DefaultThreshold is NVIDIA's recommended starting value.
	// Operators may override it via a parameter file.
	DefaultThreshold ThresholdCategory = iota

	// FixedThreshold is a hard boundary that cannot be overridden.
	// Attempts to override produce an error and the check will not
	// run with an invalid override applied.
	FixedThreshold

	// AdjustableThreshold may be tuned by operators within
	// documented bounds.
	AdjustableThreshold
)

var thresholdCategoryNames = map[ThresholdCategory]string{
	DefaultThreshold:    "DEFAULT",
	FixedThreshold:      "FIXED",
	AdjustableThreshold: "ADJUSTABLE",
}

var thresholdCategoryFromName = map[string]ThresholdCategory{
	"DEFAULT":    DefaultThreshold,
	"FIXED":      FixedThreshold,
	"ADJUSTABLE": AdjustableThreshold,
}

func (tc ThresholdCategory) String() string {
	if name, ok := thresholdCategoryNames[tc]; ok {
		return name
	}
	return fmt.Sprintf("ThresholdCategory(%d)", int(tc))
}

func (tc ThresholdCategory) MarshalJSON() ([]byte, error) {
	return marshalEnum(tc, thresholdCategoryNames)
}

func (tc *ThresholdCategory) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, thresholdCategoryFromName, tc, "threshold category")
}

// ThresholdType names the Go type a threshold's values and overrides must
// have. CEL comparisons are type-sensitive, so the catalog declares the
// type rather than inferring it from whichever value happens to be present.
type ThresholdType int

const (
	// ThresholdTypeUnspecified is the zero value and is rejected at
	// registration: unlike [ThresholdCategory], no type is safe to assume.
	ThresholdTypeUnspecified ThresholdType = iota

	ThresholdInt64
	ThresholdFloat64
	ThresholdBool
	ThresholdString
	ThresholdStringList
)

var thresholdTypeNames = map[ThresholdType]string{
	ThresholdInt64:      "INT64",
	ThresholdFloat64:    "FLOAT64",
	ThresholdBool:       "BOOL",
	ThresholdString:     "STRING",
	ThresholdStringList: "STRING_LIST",
}

var thresholdTypeFromName = map[string]ThresholdType{
	"INT64":       ThresholdInt64,
	"FLOAT64":     ThresholdFloat64,
	"BOOL":        ThresholdBool,
	"STRING":      ThresholdString,
	"STRING_LIST": ThresholdStringList,
}

func (tt ThresholdType) String() string {
	if name, ok := thresholdTypeNames[tt]; ok {
		return name
	}
	return fmt.Sprintf("ThresholdType(%d)", int(tt))
}

func (tt ThresholdType) MarshalJSON() ([]byte, error) {
	return marshalEnum(tt, thresholdTypeNames)
}

func (tt *ThresholdType) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, thresholdTypeFromName, tt, "threshold type")
}

// Ordered reports whether the type supports range bounds. Only the
// numeric types do; equality is meaningful for the rest, ordering is not.
func (tt ThresholdType) Ordered() bool {
	return tt == ThresholdInt64 || tt == ThresholdFloat64
}

// ThresholdSpec defines a threshold in a check definition.
//
// The value-bearing fields are typed any because one Thresholds map holds
// thresholds of different types; ValueType names the type they actually
// hold. Registration establishes that invariant via
// [ThresholdSpec.Normalize], so code reading a spec out of a catalog may
// assert those fields directly. An unregistered spec holds whatever literal
// its author wrote.
//
// Coercion belongs at the two boundaries that take untyped input —
// registration and override resolution — and nowhere else.
type ThresholdSpec struct {
	Value any `json:"value,omitempty"`
	// ValueMin and ValueMax are inclusive bounds on Value, every
	// PerArchitecture entry, and any operator override. Both are optional
	// and legal only when ValueType is [ThresholdType.Ordered].
	//
	// Bound a threshold only where out-of-range is unambiguously a mistake:
	// setting a tolerance very high is how an operator asks not to be
	// alerted, so those carry no ValueMax.
	ValueMin any `json:"value_min,omitempty"`
	ValueMax any `json:"value_max,omitempty"`
	// PerArchitecture overrides Value for a specific detected architecture
	// (keyed by the catalog architecture identifier, e.g. "H100"). It lets one
	// check carry per-architecture defaults — e.g. NVLink speed differs by GPU
	// generation. Architectures absent from the map fall back to Value.
	// When the check declares Architectures, registration requires this map
	// to include every listed architecture; Value may then be omitted.
	PerArchitecture map[string]any    `json:"per_architecture,omitempty"`
	Description     string            `json:"description,omitempty"`
	Unit            string            `json:"unit,omitempty"`
	Category        ThresholdCategory `json:"category"`
	// ValueType is the declared Go type of Value, the PerArchitecture
	// entries, the bounds, and any override. Mandatory — see [ThresholdSpec].
	ValueType ThresholdType `json:"value_type"`
	// RequiredOverride marks a threshold with no meaningful catalog value
	// that the operator must supply per fleet. It is independent of
	// Category, which governs whether an override is permitted at all;
	// pairing it with [FixedThreshold] is rejected at registration.
	//
	// A check whose RequiredOverride threshold is unset is skipped with
	// [ThresholdUnconfigured] rather than evaluated.
	RequiredOverride bool `json:"required_override,omitempty"`
}

// ResolvedThreshold is a threshold value after operator overrides have
// been applied. It tracks provenance so downstream systems can audit
// whether NVIDIA defaults or operator-supplied values were used.
type ResolvedThreshold struct {
	Value         any               `json:"value"`
	OriginalValue any               `json:"original_value"`
	Category      ThresholdCategory `json:"category"`
	Overridden    bool              `json:"overridden"`
}

// FixedThresholdError is returned when an operator attempts to override
// a threshold classified as Fixed.
type FixedThresholdError struct {
	ThresholdName string
	CheckID       string
}

func (e *FixedThresholdError) Error() string {
	return fmt.Sprintf(
		"cannot override fixed threshold %q on check %q: fixed thresholds are non-overridable",
		e.ThresholdName, e.CheckID,
	)
}
