// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

const (
	unknownID       = "gpu_xid_unknown"
	informationalID = "gpu_xid_informational"
)

func unknownVerdict(t *testing.T, nv health.NodeVerdict) health.CheckVerdict {
	t.Helper()
	v, ok := checkResult(nv, unknownID)
	if !ok {
		t.Fatalf("no %s verdict", unknownID)
	}
	return v
}

func TestXidUnknown_FiresOnCodeNoCheckCovers(t *testing.T) {
	nv := evalXid(t, 9999)
	v := unknownVerdict(t, nv)
	if v.Severity != health.Warning {
		t.Errorf("severity = %v, want WARNING", v.Severity)
	}
	if v.RecommendedAction.OperationalState != health.OperationalMonitor || len(v.RecommendedAction.RemediationActions) != 0 {
		t.Errorf("recommended action = %+v, want monitor with no remediation actions", v.RecommendedAction)
	}
	for _, msg := range v.Messages {
		if !strings.Contains(msg, "recommends no action") {
			t.Errorf("message %q does not say HV recommends no action", msg)
		}
	}
}

func TestXidUnknown_QuietForKnownAndIgnoredCodes(t *testing.T) {
	// 13 has a check, 63 is IGNORE in NVIDIA's catalog, 1 is Unused.
	for _, code := range []int64{13, 79, 63, 1, 172} {
		if v := unknownVerdict(t, evalXid(t, code)); v.Severity != health.Pass {
			t.Errorf("Xid %d: %s severity = %v, want PASS", code, unknownID, v.Severity)
		}
	}
}

func TestXidUnknown_ReportsOnlyUnknownEventsAndCounts(t *testing.T) {
	nv := evalXid(t, 13, 9999, 9999)
	v := unknownVerdict(t, nv)
	events, err := json.Marshal(v.ObservedValues[xid.SignalEvents])
	if err != nil {
		t.Fatal(err)
	}
	var got []xid.XidEvent
	if err := json.Unmarshal(events, &got); err != nil {
		t.Fatal(err)
	}
	// The events carry no message, so they are not grouped; both are code 9999.
	if len(got) != 2 || got[0].XidCode != 9999 || got[1].XidCode != 9999 {
		t.Errorf("events = %s, want only the two code 9999 events", events)
	}
	counts, _ := json.Marshal(v.ObservedValues[xid.SignalUnknownXidCounts])
	if string(counts) != `{"9999":2}` {
		t.Errorf("unknown_xid_counts = %s, want {\"9999\":2}", counts)
	}
}

func TestXidUnknown_OccurrencesOverride(t *testing.T) {
	cat := xidCatalog(t, occurrencesOverride(unknownID, 3))
	nv, err := cat.EvaluateAll(measurementFor(9999, 9999))
	if err != nil {
		t.Fatal(err)
	}
	if v := unknownVerdict(t, nv); v.Severity != health.Pass {
		t.Errorf("2 occurrences under a threshold of 3: severity = %v, want PASS", v.Severity)
	}
}

func TestXidUnknown_DisabledCheckStillCoversItsCode(t *testing.T) {
	cat := xidCatalog(t, catalog.WithDisabledChecks("gpu_xid_13"))
	nv, err := cat.EvaluateAll(measurementFor(13))
	if err != nil {
		t.Fatal(err)
	}
	if v := unknownVerdict(t, nv); v.Severity != health.Pass {
		t.Errorf("code of a disabled check: %s severity = %v, want PASS", unknownID, v.Severity)
	}
}

func informationalVerdict(t *testing.T, nv health.NodeVerdict) health.CheckVerdict {
	t.Helper()
	v, ok := checkResult(nv, informationalID)
	if !ok {
		t.Fatalf("no %s verdict", informationalID)
	}
	return v
}

func TestXidInformational_FiresOnInfoCodeWithoutAction(t *testing.T) {
	for _, code := range []int64{63, 172, 1} {
		v := informationalVerdict(t, evalXid(t, code))
		if v.Severity != health.Info {
			t.Errorf("Xid %d: severity = %v, want INFO", code, v.Severity)
		}
		if v.RecommendedAction.OperationalState != health.OperationalReady || len(v.RecommendedAction.RemediationActions) != 0 {
			t.Errorf("Xid %d: recommended action = %+v, want ready with no remediation actions", code, v.RecommendedAction)
		}
	}
}

func TestXidInformational_QuietForCoveredAndUnknownCodes(t *testing.T) {
	// 13 and 79 have checks, 154 has its own INFO check, 9999 is unknown.
	for _, code := range []int64{13, 79, 154, 9999} {
		if v := informationalVerdict(t, evalXid(t, code)); v.Severity != health.Pass {
			t.Errorf("Xid %d: %s severity = %v, want PASS", code, informationalID, v.Severity)
		}
	}
}

func TestXidInformational_ReportsOnlyInfoEventsAndCounts(t *testing.T) {
	nv := evalXid(t, 13, 9999, 63, 172, 172)
	v := informationalVerdict(t, nv)
	counts, _ := json.Marshal(v.ObservedValues[xid.SignalInformationalXidCounts])
	if string(counts) != `{"172":2,"63":1}` {
		t.Errorf("informational_xid_counts = %s, want {\"172\":2,\"63\":1}", counts)
	}
	events, _ := json.Marshal(v.ObservedValues[xid.SignalEvents])
	if strings.Contains(string(events), `"xid_code":13`) || strings.Contains(string(events), `"xid_code":9999`) {
		t.Errorf("events = %s, want only codes 63 and 172", events)
	}
	// The unknown check does not report the info codes either.
	u := unknownVerdict(t, nv)
	unknown, _ := json.Marshal(u.ObservedValues[xid.SignalUnknownXidCounts])
	if string(unknown) != `{"9999":1}` {
		t.Errorf("unknown_xid_counts = %s, want only 9999", unknown)
	}
}
