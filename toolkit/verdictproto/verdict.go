// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package verdictproto translates catalog verdicts to the experimental wire API.
// Dynamic values preserve JSON semantics, rather than their original Go types.
// Numbers whose JSON meaning would change are rejected rather than rounded.
package verdictproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/proto/verdictjson"
)

// ToProto converts every catalog JSON field, rejecting unknown enum names and
// dynamic numbers that google.protobuf.Value cannot carry without precision loss.
func ToProto(verdict health.NodeVerdict) (*healthv1.NodeVerdict, error) {
	if verdict.SchemaVersion != health.NodeVerdictSchemaVersion {
		return nil, fmt.Errorf("unsupported verdict schema version %q", verdict.SchemaVersion)
	}
	if verdict.CatalogVersion == "" {
		return nil, fmt.Errorf("verdict has an empty catalog version")
	}
	data, err := json.Marshal(verdict)
	if err != nil {
		return nil, fmt.Errorf("encode catalog verdict: %w", err)
	}
	if err := checkNumbers(data); err != nil {
		return nil, err
	}
	out := &healthv1.NodeVerdict{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("convert verdict to protobuf: %w", err)
	}
	return out, nil
}

// FromProto converts a wire verdict. Unknown enum numbers, unsupported schema
// versions, and missing required scalar fields fail closed.
func FromProto(verdict *healthv1.NodeVerdict) (health.NodeVerdict, error) {
	var out health.NodeVerdict
	if verdict == nil {
		return out, fmt.Errorf("nil verdict")
	}
	if verdict.GetSchemaVersion() != health.NodeVerdictSchemaVersion {
		return out, fmt.Errorf("unsupported verdict schema version %q", verdict.GetSchemaVersion())
	}
	if verdict.CatalogVersion == nil || verdict.Timestamp == nil || verdict.TraceId == nil || verdict.OverallSeverity == nil {
		return out, fmt.Errorf("verdict is missing a required scalar field")
	}
	if verdict.GetCatalogVersion() == "" {
		return out, fmt.Errorf("verdict has an empty catalog version")
	}
	if err := validateAction(verdict.GetRecommendedAction()); err != nil {
		return out, err
	}
	for _, check := range append(append([]*healthv1.CheckVerdict(nil), verdict.GetCheckResults()...), verdict.GetSkippedChecks()...) {
		if check == nil || check.Timestamp == nil || check.CheckId == nil || check.SpanId == nil || check.TraceId == nil || check.Severity == nil || check.Category == nil || check.MaturityLevel == nil || check.Skipped == nil || check.NvidiaEndorsed == nil {
			return out, fmt.Errorf("check verdict is missing a required scalar field")
		}
		if err := validateAction(check.GetRecommendedAction()); err != nil {
			return out, err
		}
		for name, threshold := range check.GetThresholds() {
			if threshold == nil || threshold.Value == nil || threshold.OriginalValue == nil || threshold.Category == nil || threshold.Overridden == nil {
				return out, fmt.Errorf("threshold %q is missing a required field", name)
			}
		}
	}
	for _, component := range verdict.GetAffectedComponents() {
		if component == nil || component.Category == nil || component.MaxSeverity == nil {
			return out, fmt.Errorf("affected component is missing a required scalar field")
		}
	}
	data, err := MarshalJSON(verdict)
	if err != nil {
		return out, err
	}
	if err := checkNumbers(data); err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("convert verdict from protobuf: %w", err)
	}
	return out, nil
}

func validateAction(action *healthv1.RecommendedAction) error {
	if action != nil && action.OperationalState == nil {
		return fmt.Errorf("recommended action is missing operational_state")
	}
	return nil
}

// MarshalJSON uses proto field names and restores required containers that
// protojson omits when empty. Protobuf does not distinguish a nil repeated
// field from an empty one; absent check results use the catalog's null spelling.
func MarshalJSON(verdict *healthv1.NodeVerdict) ([]byte, error) {
	return verdictjson.Marshal(verdict)
}

func checkNumbers(data []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return checkValue(value, "verdict")
}

func checkValue(value any, path string) error {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return fmt.Errorf("%s: invalid number: %w", path, err)
		}
		encoded, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("%s: encode number: %w", path, err)
		}
		original, originalOK := new(big.Rat).SetString(v.String())
		roundTrip, roundTripOK := new(big.Rat).SetString(string(encoded))
		if !originalOK || !roundTripOK || original.Cmp(roundTrip) != 0 {
			return fmt.Errorf("%s: number %s loses precision through google.protobuf.Value", path, v)
		}
	case map[string]any:
		for key, nested := range v {
			if err := checkValue(nested, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, nested := range v {
			if err := checkValue(nested, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
