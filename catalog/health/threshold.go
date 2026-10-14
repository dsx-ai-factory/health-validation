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

// ThresholdSpec defines a threshold in a check definition. It carries
// the catalog-authored value and its governance classification.
type ThresholdSpec struct {
	Value any `json:"value,omitempty"`
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

// ResolveThresholds merges catalog-authored thresholds with operator
// overrides for the given architecture. A threshold's PerArchitecture entry
// for architecture becomes its base value (else Value); an operator override
// then applies on top. Returns an error if an override targets a Fixed
// threshold.
func ResolveThresholds(checkID string, specs map[string]ThresholdSpec, overrides map[string]any, architecture string) (map[string]ResolvedThreshold, error) {
	resolved := make(map[string]ResolvedThreshold, len(specs))

	for name, spec := range specs {
		base := spec.Value
		if archValue, ok := spec.PerArchitecture[architecture]; ok {
			base = archValue
		}

		rt := ResolvedThreshold{
			Value:         base,
			Category:      spec.Category,
			OriginalValue: base,
		}

		if override, ok := overrides[name]; ok {
			if spec.Category == FixedThreshold {
				return nil, &FixedThresholdError{
					ThresholdName: name,
					CheckID:       checkID,
				}
			}
			rt.Value = override
			rt.Overridden = true
		}

		resolved[name] = rt
	}

	return resolved, nil
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
