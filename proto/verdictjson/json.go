// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package verdictjson emits verdict protobufs using the catalog's JSON shape.
// It depends only on the wire contract so transports need no catalog imports.
package verdictjson

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

// Marshal uses proto field names and restores required containers that
// protojson omits when empty. Protobuf cannot preserve nil versus empty lists;
// absent check results use null, while component check IDs use an empty array.
func Marshal(verdict *healthv1.NodeVerdict) ([]byte, error) {
	if verdict == nil {
		return nil, fmt.Errorf("nil verdict")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(verdict)
	if err != nil {
		return nil, fmt.Errorf("encode protobuf verdict: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if _, ok := fields["severity_by_domain"]; !ok {
		fields["severity_by_domain"] = json.RawMessage(`{}`)
	}
	if _, ok := fields["check_results"]; !ok {
		fields["check_results"] = json.RawMessage(`null`)
	}
	if encoded, ok := fields["affected_components"]; ok {
		var components []map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &components); err != nil {
			return nil, err
		}
		for _, component := range components {
			if _, ok := component["check_ids"]; !ok {
				component["check_ids"] = json.RawMessage(`[]`)
			}
		}
		encoded, err := json.Marshal(components)
		if err != nil {
			return nil, err
		}
		fields["affected_components"] = encoded
	}
	return json.Marshal(fields)
}
