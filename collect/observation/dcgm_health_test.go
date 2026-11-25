// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func healthReportBatch(report *DCGMHealthReport) *ObservationBatch {
	return &ObservationBatch{Observations: []*Observation{{
		SignalId: "dcgm.health",
		Target:   NodeTarget(""),
		Outcome:  &Observation_Value{Value: &Value{Kind: &Value_DCGMHealthReport{DcgmHealthReport: report}}},
	}}}
}

func healthEntityResult(entity *ObservationTarget, member bool, incidents ...*DCGMHealthIncident) *DCGMHealthEntityResult {
	return &DCGMHealthEntityResult{Entity: entity, GroupMember: proto.Bool(member), Incidents: incidents}
}

func TestDCGMHealthReportRoundTrip(t *testing.T) {
	unknownEntity := &ObservationTarget{Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm", EntityKind: 9876, EntityId: math.MaxUint32}}}
	gpuEntity := &ObservationTarget{Kind: &ObservationTarget_GPU{Gpu: &GPURef{Uuid: "GPU-enriched", Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1}}}}
	switchEntity := &ObservationTarget{Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Native: &NativeEntityRef{Source: "dcgm", EntityKind: 3, EntityId: 17}}}}
	batch := healthReportBatch(&DCGMHealthReport{
		RequestedSystems: math.MaxUint64,
		GroupResults: []*DCGMHealthGroupResult{
			{
				EntityKind: 1, Systems: 17, NativeOverallHealth: DCGMHealthResult(-987).Enum(),
				MonitoredSince: timestamppb.New(time.Unix(1_790_000_000, 123)), PossiblyTruncated: true,
				EntityResults: []*DCGMHealthEntityResult{
					healthEntityResult(gpuEntity, true, &DCGMHealthIncident{
						System: 16, NativeHealth: DCGMHealthResultWarn.Enum(), ErrorCode: 1, Message: "memory finding",
						NativeErrorSeverity: proto.Uint32(0), NativeErrorCategory: proto.Uint32(math.MaxUint32), Suggestion: proto.String(""),
					}),
					healthEntityResult(unknownEntity, false, &DCGMHealthIncident{System: 0xffffffff, NativeHealth: DCGMHealthResult(-123).Enum(), ErrorCode: math.MaxUint64, Message: "unknown native status"}),
					{Entity: GPUTarget("GPU-rejected"), GroupMember: proto.Bool(false), CollectionErrors: []*DCGMHealthEntityError{{
						Systems: 16, Error: &CollectionError{Source: "dcgm", Operation: "health membership", SourceError: "FUTURE_STATUS", Category: CollectionErrorCategoryUnavailable},
					}}},
				},
			},
			{
				EntityKind: 3, Systems: 3072,
				CollectionError: &CollectionError{Source: "dcgm", Operation: "health", Category: CollectionErrorCategoryUnsupported, Detail: "switch health unavailable"},
				EntityResults: []*DCGMHealthEntityResult{
					healthEntityResult(switchEntity, true, &DCGMHealthIncident{System: 1 << 63, NativeHealth: DCGMHealthResult(math.MaxInt32).Enum(), ErrorCode: 0}),
				},
			},
		},
		CollectionId:          "shared-collection",
		SystemsWithoutTargets: 1 << 62,
		SystemErrors: map[uint64]*CollectionError{
			1:       {Source: "dcgm", Operation: "health discovery", Category: CollectionErrorCategoryUnavailable, Detail: "PCIe discovery failed"},
			1 << 61: {Source: "dcgm", Operation: "health discovery", SourceError: "FUTURE_STATUS", Category: CollectionErrorCategoryUnavailable, Detail: "future system unavailable"},
		},
	})
	if err := ValidateBatch(batch); err != nil {
		t.Fatalf("ValidateBatch() error = %v", err)
	}
	jsonData, err := protojson.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonData), `"dcgmHealthReport"`) {
		t.Fatalf("JSON has no typed report branch: %s", jsonData)
	}
	if !strings.Contains(string(jsonData), `"collectionId"`) || strings.Contains(string(jsonData), `"checkId"`) {
		t.Fatalf("JSON has an incorrect report collection ID field: %s", jsonData)
	}
	binaryData, err := proto.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	for name, unmarshal := range map[string]func(*ObservationBatch) error{
		"binary": func(m *ObservationBatch) error { return proto.Unmarshal(binaryData, m) },
		"JSON":   func(m *ObservationBatch) error { return protojson.Unmarshal(jsonData, m) },
	} {
		t.Run(name, func(t *testing.T) {
			got := &ObservationBatch{}
			if err := unmarshal(got); err != nil {
				t.Fatal(err)
			}
			if err := ValidateBatch(got); err != nil {
				t.Fatalf("ValidateBatch() error = %v", err)
			}
			if !proto.Equal(batch, got) {
				t.Fatalf("native values changed after round trip: got %v, want %v", got, batch)
			}
		})
	}
}

func TestDCGMHealthPartialCoveragePreservesFindings(t *testing.T) {
	batch := healthReportBatch(&DCGMHealthReport{
		RequestedSystems: 1 | 16 | 1<<10,
		GroupResults: []*DCGMHealthGroupResult{
			{
				EntityKind: 1, Systems: 16, NativeOverallHealth: DCGMHealthResultFail.Enum(), PossiblyTruncated: true,
				EntityResults: []*DCGMHealthEntityResult{healthEntityResult(GPUTarget("GPU-0"), true, &DCGMHealthIncident{
					System: 16, NativeHealth: DCGMHealthResultFail.Enum(), ErrorCode: 42,
					Message: "memory finding",
				})},
			},
			{EntityKind: 3, Systems: 1 << 10},
		},
		SystemsWithoutTargets: 1 << 10,
		SystemErrors: map[uint64]*CollectionError{
			1: {Source: "dcgm", Operation: "health discovery", Category: CollectionErrorCategoryUnavailable, Detail: "PCIe unavailable"},
		},
	})
	if err := ValidateBatch(batch); err != nil {
		t.Fatalf("partial health report rejected: %v", err)
	}
	encoded, err := protojson.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &ObservationBatch{}
	if err := protojson.Unmarshal(encoded, decoded); err != nil {
		t.Fatal(err)
	}
	report := decoded.GetObservations()[0].GetValue().GetDcgmHealthReport()
	groups := report.GetGroupResults()
	if len(groups) != 2 {
		t.Fatalf("partial health report lost groups: %v", report)
	}
	entities := groups[0].GetEntityResults()
	if len(entities) != 1 || !entities[0].GetGroupMember() ||
		entities[0].GetEntity().GetGpu().GetUuid() != "GPU-0" || len(entities[0].GetIncidents()) != 1 ||
		entities[0].GetIncidents()[0].GetMessage() != "memory finding" ||
		report.GetSystemErrors()[1].GetDetail() != "PCIe unavailable" || report.GetSystemsWithoutTargets() != 1<<10 || !groups[0].GetPossiblyTruncated() ||
		groups[1].NativeOverallHealth != nil {
		t.Fatalf("partial health report lost findings or coverage limits: %v", report)
	}
}

func TestDCGMHealthAbsentAndZeroNativeResults(t *testing.T) {
	for _, report := range []*DCGMHealthReport{
		{},
		{RequestedSystems: 16, GroupResults: []*DCGMHealthGroupResult{{EntityKind: 1, Systems: 16}}},
		{RequestedSystems: 16, GroupResults: []*DCGMHealthGroupResult{{EntityKind: 1, Systems: 16, NativeOverallHealth: DCGMHealthResultPass.Enum()}}},
	} {
		batch := healthReportBatch(report)
		if err := ValidateBatch(batch); err != nil {
			t.Fatalf("empty incident list rejected: %v", err)
		}
		jsonData, err := protojson.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(jsonData), `"nativeOverallHealth"`); got != (len(report.GroupResults) != 0 && report.GroupResults[0].NativeOverallHealth != nil) {
			t.Fatalf("JSON lost optional native status presence: %s", jsonData)
		}
		binaryData, err := proto.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		for name, unmarshal := range map[string]func(*ObservationBatch) error{
			"binary": func(m *ObservationBatch) error { return proto.Unmarshal(binaryData, m) },
			"JSON":   func(m *ObservationBatch) error { return protojson.Unmarshal(jsonData, m) },
		} {
			t.Run(name, func(t *testing.T) {
				got := &ObservationBatch{}
				if err := unmarshal(got); err != nil {
					t.Fatal(err)
				}
				if !proto.Equal(batch, got) {
					t.Fatalf("optional zero/absent result changed: got %v, want %v", got, batch)
				}
				for _, group := range got.GetObservations()[0].GetValue().GetDcgmHealthReport().GetGroupResults() {
					if len(group.GetEntityResults()) != 0 {
						t.Fatal("empty group gained an entity result")
					}
				}
			})
		}
	}
}

func TestValidateDCGMHealthReportStructure(t *testing.T) {
	withEntities := func(entities ...*DCGMHealthEntityResult) *Value_DCGMHealthReport {
		return &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
			RequestedSystems: 16,
			GroupResults:     []*DCGMHealthGroupResult{{EntityKind: 1, Systems: 16, EntityResults: entities}},
		}}
	}
	tests := []struct {
		name      string
		kind      *Value_DCGMHealthReport
		wantError string
	}{
		{name: "typed nil wrapper", wantError: "empty DCGM health report"},
		{name: "missing report", kind: &Value_DCGMHealthReport{}, wantError: "empty DCGM health report"},
		{name: "missing entity result", kind: withEntities(nil), wantError: "group 0 entity result 0 requires an entity"},
		{name: "missing incident", kind: withEntities(healthEntityResult(GPUTarget("GPU-0"), true, nil)), wantError: "empty incident"},
		{name: "missing entity identity", kind: withEntities(&DCGMHealthEntityResult{GroupMember: proto.Bool(false)}), wantError: "requires an entity"},
		{name: "missing membership", kind: withEntities(&DCGMHealthEntityResult{Entity: GPUTarget("GPU-0")}), wantError: "requires group membership"},
		{
			name: "unknown membership after group failure",
			kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
				RequestedSystems: 16,
				GroupResults: []*DCGMHealthGroupResult{{
					EntityKind: 1, Systems: 16,
					CollectionError: &CollectionError{Source: "dcgm", Operation: "health setup", Category: CollectionErrorCategorySourceError},
					EntityResults:   []*DCGMHealthEntityResult{{Entity: GPUTarget("GPU-0")}},
				}},
			}},
		},
		{name: "invalid setup timestamp", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, GroupResults: []*DCGMHealthGroupResult{{Systems: 16, MonitoredSince: &timestamppb.Timestamp{Nanos: -1}}}}}, wantError: "monitored since"},
		{name: "missing group", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{GroupResults: []*DCGMHealthGroupResult{nil}}}, wantError: "group result 0 is required"},
		{name: "empty group systems", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, GroupResults: []*DCGMHealthGroupResult{{}}}}, wantError: "must identify requested systems"},
		{name: "unrequested group systems", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, GroupResults: []*DCGMHealthGroupResult{{Systems: 17}}}}, wantError: "must identify requested systems"},
		{name: "failed group without targets", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemsWithoutTargets: 16, GroupResults: []*DCGMHealthGroupResult{{Systems: 16, CollectionError: &CollectionError{}}}}}, wantError: "cannot also have no targets"},
		{name: "unrequested no-target system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemsWithoutTargets: 1}}, wantError: "systems_without_targets includes unrequested systems"},
		{name: "unknown requested system without targets", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 1 << 63, SystemsWithoutTargets: 1 << 63}}},
		{name: "zero failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{0: {}}}}, wantError: "key must identify one subsystem"},
		{name: "multiple failed systems in key", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 17, SystemErrors: map[uint64]*CollectionError{17: {}}}}, wantError: "key must identify one subsystem"},
		{name: "unrequested failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{1: {}}}}, wantError: "system_errors includes an unrequested system"},
		{name: "missing system error", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{16: nil}}}, wantError: "requires an error for each system"},
		{name: "failed system without targets", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemsWithoutTargets: 16, SystemErrors: map[uint64]*CollectionError{16: {}}}}, wantError: "failed system cannot also have no targets"},
		{name: "unknown requested failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 1 << 63, SystemErrors: map[uint64]*CollectionError{1 << 63: {}}}}},
		{
			name: "missing entity error",
			kind: withEntities(&DCGMHealthEntityResult{
				Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false), CollectionErrors: []*DCGMHealthEntityError{nil},
			}),
			wantError: "requires a collection error",
		},
		{
			name: "missing entity error detail",
			kind: withEntities(&DCGMHealthEntityResult{
				Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false), CollectionErrors: []*DCGMHealthEntityError{{Systems: 16}},
			}),
			wantError: "requires a collection error",
		},
		{name: "invalid entity target", kind: withEntities(healthEntityResult(NodeTarget(""), false)), wantError: "health entity must identify"},
		{
			name: "unrequested entity error systems",
			kind: withEntities(&DCGMHealthEntityResult{
				Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false),
				CollectionErrors: []*DCGMHealthEntityError{{Systems: 17, Error: &CollectionError{}}},
			}),
			wantError: "error must identify requested systems in its group",
		},
		{
			name: "empty entity error systems",
			kind: withEntities(&DCGMHealthEntityResult{
				Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false),
				CollectionErrors: []*DCGMHealthEntityError{{Error: &CollectionError{}}},
			}),
			wantError: "error must identify requested systems in its group",
		},
		{
			name: "entity error system outside group",
			kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
				RequestedSystems: 17,
				GroupResults: []*DCGMHealthGroupResult{{
					EntityKind: 1, Systems: 16,
					EntityResults: []*DCGMHealthEntityResult{{
						Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false),
						CollectionErrors: []*DCGMHealthEntityError{{Systems: 1, Error: &CollectionError{}}},
					}},
				}},
			}},
			wantError: "error must identify requested systems in its group",
		},
		{
			name: "failed entity without targets",
			kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
				RequestedSystems: 16, SystemsWithoutTargets: 16,
				GroupResults: []*DCGMHealthGroupResult{{
					EntityKind: 1, Systems: 16,
					EntityResults: []*DCGMHealthEntityResult{{
						Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false),
						CollectionErrors: []*DCGMHealthEntityError{{Systems: 16, Error: &CollectionError{}}},
					}},
				}},
			}},
			wantError: "cannot also have no targets",
		},
		{
			name: "entity error without usable members",
			kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
				RequestedSystems: 17,
				GroupResults: []*DCGMHealthGroupResult{{
					EntityKind: 1, Systems: 17,
					EntityResults: []*DCGMHealthEntityResult{{
						Entity: GPUTarget("GPU-0"), GroupMember: proto.Bool(false),
						CollectionErrors: []*DCGMHealthEntityError{{Systems: 17, Error: &CollectionError{}}},
					}},
				}},
			}},
		},
		{
			name: "repeated entity kind",
			kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{
				RequestedSystems: 16,
				GroupResults: []*DCGMHealthGroupResult{
					{EntityKind: 1, Systems: 16, EntityResults: []*DCGMHealthEntityResult{healthEntityResult(GPUTarget("GPU-0"), true)}},
					{EntityKind: 1, Systems: 16, EntityResults: []*DCGMHealthEntityResult{healthEntityResult(GPUTarget("GPU-1"), true)}},
				},
			}},
		},
		{
			name: "unknown and zero native identity",
			kind: withEntities(healthEntityResult(&ObservationTarget{
				Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm"}},
			}, false, &DCGMHealthIncident{})),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := healthReportBatch(nil)
			batch.Observations[0].GetValue().Kind = test.kind
			err := ValidateBatch(batch)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateBatch() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateBatch() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestDCGMHealthResultEncoding(t *testing.T) {
	tests := []struct {
		wantJSON string
		result   DCGMHealthResult
	}{
		{result: DCGMHealthResultPass, wantJSON: `"PASS"`},
		{result: DCGMHealthResultWarn, wantJSON: `"WARN"`},
		{result: DCGMHealthResultFail, wantJSON: `"FAIL"`},
		{result: DCGMHealthResult(987), wantJSON: `987`},
		{result: DCGMHealthResult(-987), wantJSON: `-987`},
	}
	for _, test := range tests {
		t.Run(test.result.String(), func(t *testing.T) {
			report := &DCGMHealthReport{
				RequestedSystems: 16,
				GroupResults: []*DCGMHealthGroupResult{{
					EntityKind: 1, Systems: 16, NativeOverallHealth: test.result.Enum(),
					EntityResults: []*DCGMHealthEntityResult{healthEntityResult(GPUTarget("GPU-0"), true, &DCGMHealthIncident{NativeHealth: test.result.Enum()})},
				}},
			}
			jsonData, err := protojson.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(jsonData, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["entityResults"]; present || strings.Contains(string(jsonData), `"groupId"`) {
				t.Fatalf("JSON contains a removed field: %s", jsonData)
			}
			var groups []map[string]json.RawMessage
			if err = json.Unmarshal(fields["groupResults"], &groups); err != nil {
				t.Fatal(err)
			}
			if got := string(groups[0]["nativeOverallHealth"]); got != test.wantJSON {
				t.Fatalf("native group JSON = %s, want %s", got, test.wantJSON)
			}
			var entities []struct {
				Incidents []map[string]json.RawMessage `json:"incidents"`
			}
			if err = json.Unmarshal(groups[0]["entityResults"], &entities); err != nil {
				t.Fatal(err)
			}
			if got := string(entities[0].Incidents[0]["nativeHealth"]); got != test.wantJSON {
				t.Fatalf("incident JSON = %s, want %s", got, test.wantJSON)
			}
			binaryData, err := proto.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for name, unmarshal := range map[string]func(*DCGMHealthReport) error{
				"binary": func(m *DCGMHealthReport) error { return proto.Unmarshal(binaryData, m) },
				"JSON":   func(m *DCGMHealthReport) error { return protojson.Unmarshal(jsonData, m) },
			} {
				t.Run(name, func(t *testing.T) {
					got := &DCGMHealthReport{}
					if err := unmarshal(got); err != nil {
						t.Fatal(err)
					}
					if !proto.Equal(report, got) {
						t.Fatalf("native result changed: got %v, want %v", got, report)
					}
					if err := ValidateBatch(healthReportBatch(got)); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestDCGMHealthIncidentAbsentAndPassStatus(t *testing.T) {
	for _, test := range []struct {
		status *DCGMHealthResult
		name   string
	}{
		{name: "omitted"},
		{name: "explicit PASS", status: DCGMHealthResultPass.Enum()},
	} {
		t.Run(test.name, func(t *testing.T) {
			incident := &DCGMHealthIncident{NativeHealth: test.status}
			if test.status != nil {
				incident.NativeErrorSeverity, incident.NativeErrorCategory, incident.Suggestion = proto.Uint32(0), proto.Uint32(0), proto.String("")
			}
			jsonData, err := protojson.Marshal(incident)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(jsonData, &fields); err != nil {
				t.Fatal(err)
			}
			_, present := fields["nativeHealth"]
			if present != (test.status != nil) || present && string(fields["nativeHealth"]) != `"PASS"` {
				t.Fatalf("JSON changed native status presence: %s", jsonData)
			}
			for _, field := range []string{"nativeErrorSeverity", "nativeErrorCategory", "suggestion"} {
				if _, present := fields[field]; present != (test.status != nil) {
					t.Fatalf("JSON changed metadata presence for %s: %s", field, jsonData)
				}
			}
			binaryData, err := proto.Marshal(incident)
			if err != nil {
				t.Fatal(err)
			}
			for name, unmarshal := range map[string]func(*DCGMHealthIncident) error{
				"binary": func(m *DCGMHealthIncident) error { return proto.Unmarshal(binaryData, m) },
				"JSON":   func(m *DCGMHealthIncident) error { return protojson.Unmarshal(jsonData, m) },
			} {
				t.Run(name, func(t *testing.T) {
					got := &DCGMHealthIncident{}
					if err := unmarshal(got); err != nil {
						t.Fatal(err)
					}
					if (got.NativeHealth != nil) != (test.status != nil) || !proto.Equal(incident, got) {
						t.Fatalf("native status presence changed: got %v, want %v", got, incident)
					}
				})
			}
		})
	}
}
