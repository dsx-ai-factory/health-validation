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

func TestDCGMHealthReportRoundTrip(t *testing.T) {
	unknownEntity := &ObservationTarget{Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm", EntityKind: 9876, EntityId: math.MaxUint32}}}
	gpuEntity := &ObservationTarget{Kind: &ObservationTarget_GPU{Gpu: &GPURef{Uuid: "GPU-enriched", Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1}}}}
	switchEntity := &ObservationTarget{Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Native: &NativeEntityRef{Source: "dcgm", EntityKind: 3, EntityId: 17}}}}
	batch := healthReportBatch(&DCGMHealthReport{
		RequestedSystems:         math.MaxUint64,
		GroupId:                  proto.Uint64(0),
		Entities:                 []*ObservationTarget{gpuEntity, switchEntity, unknownEntity},
		NativeGroupOverallHealth: DCGMHealthResult(-987).Enum(),
		Incidents: []*DCGMHealthIncident{
			{System: 16, NativeHealth: DCGMHealthResultWarn.Enum(), ErrorCode: 1, Message: "memory finding", Entity: gpuEntity},
			{System: 0xffffffff, NativeHealth: DCGMHealthResult(-123).Enum(), ErrorCode: math.MaxUint64, Message: "unknown native status", Entity: unknownEntity},
			{System: 1 << 63, NativeHealth: DCGMHealthResult(math.MaxInt32).Enum(), ErrorCode: 0, Entity: switchEntity},
		},
		MonitoredSince:        timestamppb.New(time.Unix(1_790_000_000, 123)),
		CollectionId:          "shared-collection",
		SystemsWithoutTargets: 1 << 62,
		SystemErrors: map[uint64]*CollectionError{
			1:       {Source: "dcgm", Operation: "health discovery", Category: CollectionErrorCategoryUnavailable, Detail: "PCIe discovery failed"},
			1 << 61: {Source: "dcgm", Operation: "health discovery", SourceError: "FUTURE_STATUS", Category: CollectionErrorCategoryUnavailable, Detail: "future system unavailable"},
		},
		PossiblyTruncated: true,
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
		RequestedSystems:         1 | 16 | 1<<10,
		GroupId:                  proto.Uint64(3),
		NativeGroupOverallHealth: DCGMHealthResultFail.Enum(),
		Incidents: []*DCGMHealthIncident{{
			System: 16, NativeHealth: DCGMHealthResultFail.Enum(), ErrorCode: 42,
			Message: "memory finding", Entity: GPUTarget("GPU-0"),
		}},
		SystemsWithoutTargets: 1 << 10,
		SystemErrors: map[uint64]*CollectionError{
			1: {Source: "dcgm", Operation: "health discovery", Category: CollectionErrorCategoryUnavailable, Detail: "PCIe unavailable"},
		},
		PossiblyTruncated: true,
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
	if len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetMessage() != "memory finding" ||
		report.GetSystemErrors()[1].GetDetail() != "PCIe unavailable" || report.GetSystemsWithoutTargets() != 1<<10 || !report.GetPossiblyTruncated() {
		t.Fatalf("partial health report lost findings or coverage limits: %v", report)
	}
}

func TestDCGMHealthAbsentAndZeroNativeResults(t *testing.T) {
	for _, report := range []*DCGMHealthReport{
		{},
		{GroupId: proto.Uint64(0), NativeGroupOverallHealth: DCGMHealthResultPass.Enum()},
	} {
		batch := healthReportBatch(report)
		if err := ValidateBatch(batch); err != nil {
			t.Fatalf("empty incident list rejected: %v", err)
		}
		jsonData, err := protojson.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(jsonData), `"nativeGroupOverallHealth"`); got != (report.NativeGroupOverallHealth != nil) {
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
				if len(got.GetObservations()[0].GetValue().GetDcgmHealthReport().GetIncidents()) != 0 {
					t.Fatal("empty report gained an incident")
				}
			})
		}
	}
}

func TestValidateDCGMHealthReportStructure(t *testing.T) {
	tests := []struct {
		name      string
		kind      *Value_DCGMHealthReport
		wantError string
	}{
		{name: "typed nil wrapper", wantError: "empty DCGM health report"},
		{name: "missing report", kind: &Value_DCGMHealthReport{}, wantError: "empty DCGM health report"},
		{name: "missing entity", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{Entities: []*ObservationTarget{nil}}}, wantError: "entity 0 is required"},
		{name: "missing incident", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{Incidents: []*DCGMHealthIncident{nil}}}, wantError: "incident 0 is required"},
		{name: "missing incident identity", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{Incidents: []*DCGMHealthIncident{{}}}}, wantError: "incident 0 entity is required"},
		{name: "invalid setup timestamp", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{MonitoredSince: &timestamppb.Timestamp{Nanos: -1}}}, wantError: "monitored since"},
		{name: "unrequested no-target system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemsWithoutTargets: 1}}, wantError: "systems_without_targets includes unrequested systems"},
		{name: "unknown requested system without targets", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 1 << 63, SystemsWithoutTargets: 1 << 63}}},
		{name: "zero failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{0: {}}}}, wantError: "key must identify one subsystem"},
		{name: "multiple failed systems in key", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 17, SystemErrors: map[uint64]*CollectionError{17: {}}}}, wantError: "key must identify one subsystem"},
		{name: "unrequested failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{1: {}}}}, wantError: "system_errors includes an unrequested system"},
		{name: "missing system error", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemErrors: map[uint64]*CollectionError{16: nil}}}, wantError: "requires an error for each system"},
		{name: "failed system without targets", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 16, SystemsWithoutTargets: 16, SystemErrors: map[uint64]*CollectionError{16: {}}}}, wantError: "failed system cannot also have no targets"},
		{name: "unknown requested failed system", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{RequestedSystems: 1 << 63, SystemErrors: map[uint64]*CollectionError{1 << 63: {}}}}},
		{name: "unknown and zero native identity", kind: &Value_DCGMHealthReport{DcgmHealthReport: &DCGMHealthReport{Incidents: []*DCGMHealthIncident{{Entity: &ObservationTarget{Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm"}}}}}}}},
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
				NativeGroupOverallHealth: test.result.Enum(),
				Incidents:                []*DCGMHealthIncident{{NativeHealth: test.result.Enum(), Entity: GPUTarget("GPU-0")}},
			}
			jsonData, err := protojson.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(jsonData, &fields); err != nil {
				t.Fatal(err)
			}
			if got := string(fields["nativeGroupOverallHealth"]); got != test.wantJSON {
				t.Fatalf("native group JSON = %s, want %s", got, test.wantJSON)
			}
			var incidents []map[string]json.RawMessage
			if err = json.Unmarshal(fields["incidents"], &incidents); err != nil {
				t.Fatal(err)
			}
			if got := string(incidents[0]["nativeHealth"]); got != test.wantJSON {
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
			incident := &DCGMHealthIncident{NativeHealth: test.status, Entity: GPUTarget("GPU-0")}
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
