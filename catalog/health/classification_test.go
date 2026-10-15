// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionContextFromName(t *testing.T) {
	tests := []struct {
		name    string
		want    ExecutionContext
		wantErr bool
	}{
		{name: "OPERATOR_INFRA", want: OperatorInfra},
		{name: "TENANT_GUEST", want: TenantGuest},
		{name: "OUT_OF_BAND", want: OutOfBand},
		{name: "tenant_guest", wantErr: true},
		{name: "UNKNOWN", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExecutionContextFromName(tt.name)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "unknown execution context") {
					t.Fatalf("ExecutionContextFromName(%q) error = %v, want unknown context error", tt.name, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExecutionContextFromName(%q): %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("ExecutionContextFromName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestCategoryContract(t *testing.T) {
	tests := []struct {
		name     string
		jsonName string
		category Category
		ordinal  int
	}{
		{name: "GPU", jsonName: "gpu", category: GPU, ordinal: 0},
		{name: "NVLink", jsonName: "nvlink", category: NVLink, ordinal: 1},
		{name: "PCIe", jsonName: "pcie", category: PCIe, ordinal: 2},
		{name: "InfiniBand", jsonName: "infiniband", category: InfiniBand, ordinal: 3},
		{name: "Storage", jsonName: "storage", category: Storage, ordinal: 4},
		{name: "Node", jsonName: "node", category: Node, ordinal: 5},
		{name: "Xid", jsonName: "xid", category: Xid, ordinal: 6},
		{name: "CPU", jsonName: "cpu", category: CPU, ordinal: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int(tt.category); got != tt.ordinal {
				t.Fatalf("ordinal = %d, want %d", got, tt.ordinal)
			}
			if got := tt.category.String(); got != tt.jsonName {
				t.Errorf("String = %q, want %q", got, tt.jsonName)
			}

			data, err := json.Marshal(tt.category)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got, want := string(data), `"`+tt.jsonName+`"`; got != want {
				t.Errorf("Marshal = %s, want %s", got, want)
			}

			var roundTrip Category
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if roundTrip != tt.category {
				t.Errorf("round trip = %v, want %v", roundTrip, tt.category)
			}
		})
	}
}

func TestCategoryMapKeyJSON(t *testing.T) {
	data, err := json.Marshal(map[Category]Severity{
		GPU:  Warning,
		Node: Pass,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var fields map[string]Severity
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Unmarshal string-keyed map: %v", err)
	}
	if got := fields["gpu"]; got != Warning {
		t.Errorf("gpu severity = %v, want WARNING", got)
	}
	if got := fields["node"]; got != Pass {
		t.Errorf("node severity = %v, want PASS", got)
	}
	if _, ok := fields["0"]; ok {
		t.Errorf("numeric category key present in JSON: %s", data)
	}

	var roundTrip map[Category]Severity
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal category-keyed map: %v", err)
	}
	if got := roundTrip[GPU]; got != Warning {
		t.Errorf("round-trip gpu severity = %v, want WARNING", got)
	}
	if got := roundTrip[Node]; got != Pass {
		t.Errorf("round-trip node severity = %v, want PASS", got)
	}
}

func TestCategoryMapKeyJSON_RejectsNumericKey(t *testing.T) {
	var got map[Category]Severity
	if err := json.Unmarshal([]byte(`{"0":"WARNING"}`), &got); err == nil {
		t.Fatal("Unmarshal numeric category key succeeded, want error")
	}
}

func TestMaturityLevelContract(t *testing.T) {
	tests := []struct {
		name     string
		jsonName string
		maturity MaturityLevel
		ordinal  int
	}{
		{name: "Stable", jsonName: "stable", maturity: Stable, ordinal: 0},
		{name: "Experimental", jsonName: "experimental", maturity: Experimental, ordinal: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int(tt.maturity); got != tt.ordinal {
				t.Fatalf("ordinal = %d, want %d", got, tt.ordinal)
			}
			if got := tt.maturity.String(); got != tt.jsonName {
				t.Errorf("String = %q, want %q", got, tt.jsonName)
			}

			data, err := json.Marshal(tt.maturity)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got, want := string(data), `"`+tt.jsonName+`"`; got != want {
				t.Errorf("Marshal = %s, want %s", got, want)
			}

			var roundTrip MaturityLevel
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if roundTrip != tt.maturity {
				t.Errorf("round trip = %v, want %v", roundTrip, tt.maturity)
			}
		})
	}
}

func TestMaturityLevel_UnmarshalInvalid(t *testing.T) {
	var ml MaturityLevel
	if err := json.Unmarshal([]byte(`"BOGUS"`), &ml); err == nil {
		t.Error("Unmarshal of unknown maturity level: want error, got nil")
	}
}

// NodeScope is the zero value, so a check that omits ConditionScope is treated
// as node-local, the way every consumer already treats every check today. A
// forgotten field can never widen a condition's blast radius.
func TestConditionScopeZeroValueIsNodeScope(t *testing.T) {
	var zero ConditionScope
	if zero != NodeScope {
		t.Fatalf("zero value = %v, want NodeScope", zero)
	}
}

func TestConditionScopeContract(t *testing.T) {
	tests := []struct {
		name     string
		jsonName string
		scope    ConditionScope
		ordinal  int
	}{
		{name: "NodeScope", jsonName: "NODE", scope: NodeScope, ordinal: 0},
		{name: "DomainScope", jsonName: "DOMAIN", scope: DomainScope, ordinal: 1},
		{name: "ClusterScope", jsonName: "CLUSTER", scope: ClusterScope, ordinal: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int(tt.scope); got != tt.ordinal {
				t.Fatalf("ordinal = %d, want %d", got, tt.ordinal)
			}
			if got := tt.scope.String(); got != tt.jsonName {
				t.Errorf("String = %q, want %q", got, tt.jsonName)
			}

			data, err := json.Marshal(tt.scope)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got, want := string(data), `"`+tt.jsonName+`"`; got != want {
				t.Errorf("Marshal = %s, want %s", got, want)
			}

			var roundTrip ConditionScope
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if roundTrip != tt.scope {
				t.Errorf("round trip = %v, want %v", roundTrip, tt.scope)
			}
		})
	}
}

// The error names the type, so a copy-pasted typeName from a neighboring enum
// is caught here rather than surfacing to a consumer decoding a verdict.
func TestConditionScope_UnmarshalInvalid(t *testing.T) {
	var cs ConditionScope
	err := json.Unmarshal([]byte(`"RACK"`), &cs)
	if err == nil {
		t.Fatal("Unmarshal of unknown condition scope: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown condition scope") {
		t.Errorf("error = %q, want it to name the type", err)
	}
}
