// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func xidCatalog(t *testing.T, opts ...catalog.Option) *catalog.Catalog {
	return xidCatalogFor(t, "GB200", opts...)
}

func xidCatalogFor(t *testing.T, architecture string, opts ...catalog.Option) *catalog.Catalog {
	t.Helper()
	defaults := []catalog.Option{
		catalog.WithArchitecture(architecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	cat := catalog.NewEmpty(append(defaults, opts...)...)
	for _, cd := range xid.Checks() {
		cat.MustRegister(cd)
	}
	return cat
}

func thresholdOverride(checkID, thresholdName string, value int64) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       checkID,
		ThresholdName: thresholdName,
		Value:         value,
	}))
}

func occurrencesOverride(checkID string, occurrences int64) catalog.Option {
	return thresholdOverride(checkID, "occurrences", occurrences)
}

// measurementFor builds an XidMeasurement directly from raw Xid codes —
// the catalog-native way, with no dmesg parsing. Parsing "NVRM: Xid …"
// log lines is the toolkit's concern (toolkit/xid.ParseXid) and is tested
// there; the catalog only needs the aggregated XidCounts/EventCount.
func measurementFor(codes ...int64) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: "boot"}
	for _, code := range codes {
		m.Events = append(m.Events, xid.XidEvent{PCIAddress: "0000:01:00", XidCode: code, Pid: 1, ProcessName: "app"})
		if m.XidCounts == nil {
			m.XidCounts = make(map[int64]int64)
		}
		m.XidCounts[code]++
		m.EventCount++
	}
	return m
}

func evalXid(t *testing.T, codes ...int64) health.NodeVerdict {
	return evalXidFor(t, "GB200", codes...)
}

func evalXidFor(t *testing.T, architecture string, codes ...int64) health.NodeVerdict {
	t.Helper()
	nv, err := xidCatalogFor(t, architecture).EvaluateAll(measurementFor(codes...))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	return nv
}

func TestXidErrors_ArchitectureCoverage(t *testing.T) {
	tests := []struct {
		name          string
		checkID       string
		architectures []string
	}{
		{name: "all generations", checkID: "gpu_xid_46", architectures: []string{"GB200", "GB300", "B200", "H100", "H200", "A100"}},
		{name: "HGX and PCIe baseboards, not Grace", checkID: "gpu_xid_54", architectures: []string{"B200", "H100", "H200", "A100"}},
		{name: "Hopper only", checkID: "gpu_xid_136", architectures: []string{"H100", "H200"}},
		{name: "Hopper and Blackwell", checkID: "gpu_xid_110", architectures: []string{"GB200", "GB300", "B200", "H100", "H200"}},
		{name: "Grace Blackwell only", checkID: "gpu_xid_142", architectures: []string{"GB200", "GB300"}},
		{name: "NVLink5 remains Blackwell only", checkID: "gpu_xid_144", architectures: []string{"GB200", "GB300", "B200"}},
		{name: "common warning", checkID: "gpu_xid_8", architectures: []string{"GB200", "GB300", "B200", "H100", "H200", "A100"}},
		{name: "Ampere and Hopper warning", checkID: "gpu_xid_80", architectures: []string{"H100", "H200", "A100"}},
		{name: "Hopper and Blackwell warning", checkID: "gpu_xid_96", architectures: []string{"GB200", "GB300", "B200", "H100", "H200"}},
		{name: "Blackwell warning", checkID: "gpu_xid_126", architectures: []string{"GB200", "GB300", "B200"}},
		{name: "informational all generations", checkID: "gpu_xid_154", architectures: []string{"GB200", "GB300", "B200", "H100", "H200", "A100"}},
	}

	definitions := make(map[string]health.CheckDefinition)
	for _, def := range xid.Checks() {
		definitions[def.ID] = def
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, ok := definitions[tt.checkID]
			if !ok {
				t.Fatalf("check %q not found", tt.checkID)
			}
			if !slices.Equal(def.Architectures, tt.architectures) {
				t.Errorf("Architectures = %v, want %v", def.Architectures, tt.architectures)
			}
		})
	}
}

// nvlink5Checks lists the Xid 144-150 checks, which split occurrences
// into fatal_occurrences and nonfatal_occurrences.
var nvlink5Checks = map[string]bool{
	"gpu_xid_144": true,
	"gpu_xid_145": true,
	"gpu_xid_146": true,
	"gpu_xid_147": true,
	"gpu_xid_148": true,
	"gpu_xid_149": true,
	"gpu_xid_150": true,
}

func TestXidErrors_AllChecksHaveOccurrencesThreshold(t *testing.T) {
	for _, def := range xid.Checks() {
		if nvlink5Checks[def.ID] {
			continue
		}
		spec, ok := def.Thresholds["occurrences"]
		if !ok {
			t.Errorf("%s: missing occurrences threshold", def.ID)
			continue
		}
		if spec.Value != int64(1) {
			t.Errorf("%s: occurrences default = %v, want 1", def.ID, spec.Value)
		}
		if spec.ValueType != health.ThresholdInt64 {
			t.Errorf("%s: occurrences value type = %v, want ThresholdInt64", def.ID, spec.ValueType)
		}
		if spec.Category != health.DefaultThreshold {
			t.Errorf("%s: occurrences category = %v, want DefaultThreshold", def.ID, spec.Category)
		}
		if spec.Unit != "count" {
			t.Errorf("%s: occurrences unit = %q, want count", def.ID, spec.Unit)
		}
	}
}

func TestXidErrors_NVLink5ChecksHaveSeparateOccurrencesThresholds(t *testing.T) {
	for _, def := range xid.Checks() {
		if !nvlink5Checks[def.ID] {
			continue
		}
		if len(def.Thresholds) != 2 {
			t.Errorf("%s: thresholds = %v, want fatal_occurrences and nonfatal_occurrences", def.ID, def.Thresholds)
		}
		for _, name := range []string{"fatal_occurrences", "nonfatal_occurrences"} {
			spec, ok := def.Thresholds[name]
			if !ok {
				t.Errorf("%s: missing %s threshold", def.ID, name)
				continue
			}
			if spec.Value != int64(1) {
				t.Errorf("%s: %s default = %v, want 1", def.ID, name, spec.Value)
			}
			if spec.ValueType != health.ThresholdInt64 {
				t.Errorf("%s: %s value type = %v, want ThresholdInt64", def.ID, name, spec.ValueType)
			}
			if spec.Category != health.DefaultThreshold {
				t.Errorf("%s: %s category = %v, want DefaultThreshold", def.ID, name, spec.Category)
			}
			if spec.Unit != "count" {
				t.Errorf("%s: %s unit = %q, want count", def.ID, name, spec.Unit)
			}
		}
	}
}

func TestXidErrors_OccurrencesOverride(t *testing.T) {
	tests := []struct {
		name         string
		checkID      string
		code         int64
		wantSeverity health.Severity
	}{
		{name: "critical", checkID: "gpu_xid_79", code: 79, wantSeverity: health.Critical},
		{name: "warning", checkID: "gpu_xid_8", code: 8, wantSeverity: health.Warning},
		{name: "xid 45 alone", checkID: "gpu_xid_45", code: 45, wantSeverity: health.Warning},
		{name: "informational", checkID: "gpu_xid_154", code: 154, wantSeverity: health.Info},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := xidCatalog(t, occurrencesOverride(tt.checkID, 2))
			for _, tc := range []struct {
				name string
				m    xid.XidMeasurement
				want health.Severity
			}{
				{name: "below threshold", m: measurementFor(tt.code), want: health.Pass},
				{name: "at threshold", m: measurementFor(tt.code, tt.code), want: tt.wantSeverity},
			} {
				t.Run(tc.name, func(t *testing.T) {
					v, err := cat.Evaluate(tt.checkID, tc.m)
					if err != nil {
						t.Fatal(err)
					}
					if v.Severity != tc.want {
						t.Errorf("Severity = %v, want %v", v.Severity, tc.want)
					}
					resolved := v.Thresholds["occurrences"]
					if resolved.Value != int64(2) || resolved.OriginalValue != int64(1) || !resolved.Overridden {
						t.Errorf("resolved occurrences = %+v, want overridden 2 from default 1", resolved)
					}
				})
			}
		})
	}
}

func TestXidErrors_WarningArchitectureMatrix(t *testing.T) {
	tests := []struct {
		architectures []string
		codes         []int64
	}{
		{
			architectures: []string{"GB200", "GB300", "B200", "H100", "H200", "A100"},
			codes:         []int64{8, 11, 13, 25, 31, 32, 39, 40, 41, 45, 60, 68, 69, 70, 71, 72, 75, 76, 77, 78, 82, 83, 84, 85, 86, 88, 89, 94},
		},
		{architectures: []string{"H100", "H200", "A100"}, codes: []int64{80}},
		{architectures: []string{"GB200", "GB300", "B200", "H100", "H200"}, codes: []int64{96, 97, 98, 99, 100, 101, 102, 103, 104, 105}},
		{architectures: []string{"GB200", "GB300", "B200"}, codes: []int64{126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 139}},
	}

	definitions := make(map[string]health.CheckDefinition)
	for _, def := range xid.Checks() {
		definitions[def.ID] = def
	}
	for _, tt := range tests {
		for _, code := range tt.codes {
			id := fmt.Sprintf("gpu_xid_%d", code)
			def, ok := definitions[id]
			if !ok {
				t.Errorf("check %q not found", id)
				continue
			}
			if !slices.Equal(def.Architectures, tt.architectures) {
				t.Errorf("%s Architectures = %v, want %v", id, def.Architectures, tt.architectures)
			}
		}
	}
}

func TestXidErrors_BlackwellWarningSkipsOnHopper(t *testing.T) {
	nv := evalXidFor(t, "H100", 126)
	for _, v := range nv.SkippedChecks {
		if v.CheckID != "gpu_xid_126" {
			continue
		}
		if v.SkipReason != health.ArchitectureUnsupported {
			t.Errorf("SkipReason = %v, want %v", v.SkipReason, health.ArchitectureUnsupported)
		}
		return
	}
	t.Fatal("gpu_xid_126 architecture skip not found")
}

func TestXidErrors_NewArchitectureSpecificChecks(t *testing.T) {
	tests := []struct {
		name         string
		architecture string
		checkID      string
		wantAction   health.RecommendedAction
		code         int64
		wantSeverity health.Severity
	}{
		{name: "Xid 54 on A100", architecture: "A100", checkID: "gpu_xid_54", code: 54, wantSeverity: health.Critical, wantAction: health.Drain(health.RemediationActionServiceRequired)},
		{name: "Xid 80 on H200", architecture: "H200", checkID: "gpu_xid_80", code: 80, wantSeverity: health.Warning, wantAction: health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired)},
		{name: "Xid 136 on H100", architecture: "H100", checkID: "gpu_xid_136", code: 136, wantSeverity: health.Critical, wantAction: resetAndServiceForTest()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := checkResult(evalXidFor(t, tt.architecture, tt.code), tt.checkID)
			if !ok {
				t.Fatalf("%s verdict not found", tt.checkID)
			}
			if v.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.wantAction)
			}
		})
	}
}

func resetAndServiceForTest() health.RecommendedAction {
	return health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
}

func TestXidErrors_SeverityByBucket(t *testing.T) {
	tests := []struct {
		name string
		code int64
		want health.Severity
	}{
		{"reset_gpu (46)", 46, health.Critical},
		{"fell off bus (79)", 79, health.Critical},
		{"memory remapping failure (64)", 64, health.Critical},
		{"double bit ECC error (48)", 48, health.Critical},
		{"app fault (11)", 11, health.Warning},
		{"graphics engine exception (13)", 13, health.Warning},
		{"memory page fault (31)", 31, health.Warning},
		{"vgpu start (78)", 78, health.Warning},
		{"preemptive removal (45)", 45, health.Warning},
		{"key rotation (151)", 151, health.Critical},
		{"recovery action changed (154)", 154, health.Info},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nv := evalXid(t, tc.code)
			if nv.OverallSeverity != tc.want {
				t.Errorf("code %d: OverallSeverity = %v, want %v", tc.code, nv.OverallSeverity, tc.want)
			}
		})
	}
}

func TestXidErrors_WarningActionByBucket(t *testing.T) {
	tests := []struct {
		name string
		want health.RecommendedAction
		code int64
	}{
		{"app fault (13)", health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired), 13},
		{"memory page fault (31)", health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired), 31},
		{"vgpu start (78)", health.Drain(health.RemediationActionUpdateSWFW), 78},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := checkResult(evalXid(t, tc.code), fmt.Sprintf("gpu_xid_%d", tc.code))
			if !ok {
				t.Fatalf("gpu_xid_%d verdict not found", tc.code)
			}
			if !v.RecommendedAction.Equal(tc.want) {
				t.Errorf("code %d: RecommendedAction = %v, want %v", tc.code, v.RecommendedAction, tc.want)
			}
			wantCode := fmt.Sprintf("Xid %d", tc.code)
			if len(v.Messages) != 1 || !strings.Contains(v.Messages[0], wantCode) {
				t.Errorf("code %d: Messages = %q, want one message containing %q", tc.code, v.Messages, wantCode)
			}
		})
	}
}

func TestXidErrors_Xid45Workflow(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		codes        []int64
		action       health.RecommendedAction
		wantSeverity health.Severity
	}{
		{
			name:         "alone",
			codes:        []int64{45},
			wantSeverity: health.Warning,
			action:       health.Drain(health.RemediationActionRestartApp),
			message:      "GPU Xid 45 occurred alone; restart the application. If Xid 45 recurs with no other Xid, report the issue.",
		},
		{
			name:         "with another Xid",
			codes:        []int64{45, 46},
			wantSeverity: health.Info,
			action:       health.NoAction,
			message:      "GPU Xid 45 accompanied another Xid; no Xid 45-specific remediation is needed. Follow the accompanying Xid's guidance.",
		},
		{
			name:         "with an Xid that has no check",
			codes:        []int64{45, 137},
			wantSeverity: health.Info,
			action:       health.NoAction,
			message:      "GPU Xid 45 accompanied another Xid; no Xid 45-specific remediation is needed. Follow the accompanying Xid's guidance.",
		},
		{
			name:         "Xid 154 does not count as a companion",
			codes:        []int64{45, 154},
			wantSeverity: health.Warning,
			action:       health.Drain(health.RemediationActionRestartApp),
			message:      "GPU Xid 45 occurred alone; restart the application. If Xid 45 recurs with no other Xid, report the issue.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := checkResult(evalXid(t, tt.codes...), "gpu_xid_45")
			if !ok {
				t.Fatal("gpu_xid_45 verdict not found")
			}
			if v.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !v.RecommendedAction.Equal(tt.action) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.action)
			}
			if len(v.Messages) != 1 || v.Messages[0] != tt.message {
				t.Errorf("Messages = %q, want [%q]", v.Messages, tt.message)
			}
		})
	}
}

func TestXidErrors_Xid151IsCritical(t *testing.T) {
	v, ok := checkResult(evalXid(t, 151), "gpu_xid_151")
	if !ok {
		t.Fatalf("gpu_xid_151 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want %v", v.Severity, health.Critical)
	}
	want := health.Drain(health.RemediationActionRestartOS)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

// nvlink5MeasurementFor builds an XidMeasurement for one NVLink5-family
// event (144-150), the way toolkit/xid.ParseXid would after decoding the
// extended Fatal/Nonfatal log format.
func nvlink5MeasurementFor(code int64, fatal bool) xid.XidMeasurement {
	m := xid.XidMeasurement{
		Since:     "boot",
		XidCounts: map[int64]int64{code: 1},
		Events: []xid.XidEvent{{
			PCIAddress: "0000:01:00",
			XidCode:    code,
			NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Fatal: fatal, Link: 8},
		}},
		EventCount: 1,
	}
	if fatal {
		m.FatalXidCounts = map[int64]int64{code: 1}
	}
	return m
}

func TestXidErrors_NVLink5_FatalIsCritical(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(146, true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
	v, ok := checkResult(nv, "gpu_xid_146")
	if !ok {
		t.Fatalf("gpu_xid_146 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_146 severity = %v, want CRITICAL", v.Severity)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestXidErrors_NVLink5_NonfatalIsWarningNoAction(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(146, false))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Warning {
		t.Errorf("OverallSeverity = %v, want WARNING", nv.OverallSeverity)
	}
	v, ok := checkResult(nv, "gpu_xid_146")
	if !ok {
		t.Fatalf("gpu_xid_146 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("gpu_xid_146 severity = %v, want WARNING", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

// A Fatal occurrence also counts toward xid_counts; it must not
// additionally be reported as Nonfatal (issue #126).
func TestXidErrors_NVLink5_FatalIsNotAlsoNonfatal(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(149, true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149")
	if !ok {
		t.Fatalf("gpu_xid_149 verdict not found")
	}
	want := []string{"GPU Xid 149 detected: NVLink NETIR error (Fatal)."}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

func TestXidErrors_NVLink5_FatalAndNonfatalReportsBoth(t *testing.T) {
	m := nvlink5MeasurementFor(149, true)
	m.XidCounts[149] = 2
	m.EventCount = 2
	m.Events = append(m.Events, xid.XidEvent{
		PCIAddress: "0000:01:00",
		XidCode:    149,
		NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Link: 9},
	})
	nv, err := xidCatalog(t).EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149")
	if !ok {
		t.Fatalf("gpu_xid_149 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_149 severity = %v, want CRITICAL", v.Severity)
	}
	want := []string{
		"GPU Xid 149 detected: NVLink NETIR error (Fatal).",
		"GPU Xid 149 detected: NVLink NETIR error (Nonfatal).",
	}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

func TestXidErrors_NVLink5_OccurrencesCountFatalAndNonfatalSeparately(t *testing.T) {
	m := nvlink5MeasurementFor(149, true)
	m.XidCounts[149] = 2
	m.EventCount = 2
	m.Events = append(m.Events, xid.XidEvent{
		PCIAddress: "0000:01:00",
		XidCode:    149,
		NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Link: 9},
	})

	// WithOverrides replaces the whole set, so both thresholds go in one.
	cat := xidCatalog(t, catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "gpu_xid_149", ThresholdName: "fatal_occurrences", Value: int64(2)},
		catalog.Override{CheckID: "gpu_xid_149", ThresholdName: "nonfatal_occurrences", Value: int64(2)},
	)))
	v, err := cat.Evaluate("gpu_xid_149", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("one Fatal plus one Nonfatal severity = %v, want PASS", v.Severity)
	}

	m.XidCounts[149] = 3
	m.FatalXidCounts[149] = 2
	m.EventCount = 3
	m.Events = append(m.Events, xid.XidEvent{
		PCIAddress: "0000:01:00",
		XidCode:    149,
		NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Fatal: true, Link: 10},
	})
	v, err = cat.Evaluate("gpu_xid_149", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("two Fatal plus one Nonfatal severity = %v, want CRITICAL", v.Severity)
	}
	want := []string{"GPU Xid 149 detected: NVLink NETIR error (Fatal)."}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

func TestXidErrors_NVLink5_OccurrencesThresholdsCanBeOverriddenIndependently(t *testing.T) {
	m := nvlink5MeasurementFor(149, true)
	m.XidCounts[149] = 2
	m.EventCount = 2
	m.Events = append(m.Events, xid.XidEvent{
		PCIAddress: "0000:01:00",
		XidCode:    149,
		NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Link: 9},
	})

	tests := []struct {
		name         string
		option       catalog.Option
		wantMessage  string
		wantSeverity health.Severity
	}{
		{
			name:         "raising Nonfatal threshold leaves Fatal detection unchanged",
			option:       thresholdOverride("gpu_xid_149", "nonfatal_occurrences", 2),
			wantSeverity: health.Critical,
			wantMessage:  "GPU Xid 149 detected: NVLink NETIR error (Fatal).",
		},
		{
			name:         "raising Fatal threshold leaves Nonfatal detection unchanged",
			option:       thresholdOverride("gpu_xid_149", "fatal_occurrences", 2),
			wantSeverity: health.Warning,
			wantMessage:  "GPU Xid 149 detected: NVLink NETIR error (Nonfatal).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := xidCatalog(t, tt.option).Evaluate("gpu_xid_149", m)
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !slices.Equal(v.Messages, []string{tt.wantMessage}) {
				t.Errorf("Messages = %q, want [%q]", v.Messages, tt.wantMessage)
			}
		})
	}
}

func TestXidErrors_NVLink5_ZeroNonfatalThresholdDoesNotMatchFatalOccurrence(t *testing.T) {
	v, err := xidCatalog(
		t,
		thresholdOverride("gpu_xid_149", "nonfatal_occurrences", 0),
	).Evaluate("gpu_xid_149", nvlink5MeasurementFor(149, true))
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := []string{"GPU Xid 149 detected: NVLink NETIR error (Fatal)."}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

// intrInfoForSubXidCode builds an IntrInfo register value whose
// fault-subtype bits (IntrInfo[25:20]) encode subcode's digit, e.g.
// "149.4" -> "0x00400000", so a test measurement carries the raw field
// a sensor would and sub_xid_code_counts is derived from it.
func intrInfoForSubXidCode(subcode string) string {
	_, digit, _ := strings.Cut(subcode, ".")
	v, err := strconv.ParseUint(digit, 16, 8)
	if err != nil {
		panic("intrInfoForSubXidCode: bad subcode " + subcode)
	}
	return fmt.Sprintf("0x%08x", v<<20)
}

// nvlink5SubXidCodeMeasurementFor builds an XidMeasurement for one Xid 149
// NETIR_LINK_EVT/NETIR_LINK_DOWN event carrying the given subcode, the way
// toolkit/xid.ParseXid would after decoding the extended log format.
func nvlink5SubXidCodeMeasurementFor(subcode string, fatal bool) xid.XidMeasurement {
	m := xid.XidMeasurement{
		Since:     "boot",
		XidCounts: map[int64]int64{149: 1},
		Events: []xid.XidEvent{{
			PCIAddress: "0000:01:00",
			XidCode:    149,
			NVLink5: &xid.NVLink5Detail{
				SubComponent: "NETIR_LINK_EVT",
				IntrInfo:     intrInfoForSubXidCode(subcode),
				Fatal:        fatal,
				Link:         8,
			},
		}},
		EventCount: 1,
	}
	if fatal {
		m.FatalXidCounts = map[int64]int64{149: 1}
	}
	return m
}

func TestXidErrors_NVLink5SubXidCode_Critical(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.4", true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_4")
	if !ok {
		t.Fatalf("gpu_xid_149_4 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_149_4 severity = %v, want CRITICAL", v.Severity)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestXidErrors_NVLink5SubXidCode_Warning(t *testing.T) {
	// Subcode 149.26 is one of the two rows (with 149.28) whose
	// decode-table resolution is IGNORE rather than RESET_GPU.
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.26", false))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_26")
	if !ok {
		t.Fatalf("gpu_xid_149_26 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("gpu_xid_149_26 severity = %v, want WARNING", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

func TestXidErrors_NVLink5SubXidCode_OccurrencesOverride(t *testing.T) {
	m := nvlink5SubXidCodeMeasurementFor("149.4", true)
	cat := xidCatalog(t, occurrencesOverride("gpu_xid_149_4", 2))

	nv, err := cat.EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_4")
	if !ok {
		t.Fatal("gpu_xid_149_4 verdict not found")
	}
	if v.Severity != health.Pass {
		t.Errorf("one occurrence severity = %v, want PASS", v.Severity)
	}

	m.Events = append(m.Events, m.Events[0])
	m.XidCounts[149] = 2
	m.FatalXidCounts[149] = 2
	m.EventCount = 2
	nv, err = cat.EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	v, ok = checkResult(nv, "gpu_xid_149_4")
	if !ok {
		t.Fatal("gpu_xid_149_4 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("two occurrences severity = %v, want CRITICAL", v.Severity)
	}
}

func TestXidErrors_NVLink5SubXidCode_AdditiveWithBaseline(t *testing.T) {
	// A "149.4" Fatal event fires both the per-subcode check and the
	// baseline gpu_xid_149 Fatal/Nonfatal check: subcode checks are
	// additive, not a replacement.
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.4", true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	for _, id := range []string{"gpu_xid_149", "gpu_xid_149_4"} {
		v, ok := checkResult(nv, id)
		if !ok {
			t.Fatalf("%s verdict not found", id)
		}
		if v.Severity != health.Critical {
			t.Errorf("%s severity = %v, want CRITICAL", id, v.Severity)
		}
	}
}

// TestXidErrors_NVLink5SubXidCode_OnlyItsOwnDigit pins that a subcode
// check fires for its own digit alone, and not at all for a sub-component
// it was not built from.
func TestXidErrors_NVLink5SubXidCode_OnlyItsOwnDigit(t *testing.T) {
	ber := nvlink5SubXidCodeMeasurementFor("149.4", true)
	ber.Events[0].NVLink5.SubComponent = "NETIR_BER_EVENT"

	tests := []struct {
		name string
		want string
		m    xid.XidMeasurement
	}{
		{name: "149.4 fires only gpu_xid_149_4", m: nvlink5SubXidCodeMeasurementFor("149.4", true), want: "gpu_xid_149_4"},
		{name: "unchecked digit fires none", m: nvlink5SubXidCodeMeasurementFor("149.0", true)},
		{name: "other sub-component fires none", m: ber},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nv, err := xidCatalog(t).EvaluateAll(tt.m)
			if err != nil {
				t.Fatalf("EvaluateAll: %v", err)
			}
			for _, v := range nv.CheckResults {
				if !strings.HasPrefix(v.CheckID, "gpu_xid_149_") || v.CheckID == tt.want {
					continue
				}
				if v.Severity != health.Pass {
					t.Errorf("%s severity = %v, want PASS", v.CheckID, v.Severity)
				}
			}
		})
	}
}

func checkResult(nv health.NodeVerdict, checkID string) (health.CheckVerdict, bool) {
	for _, v := range nv.CheckResults {
		if v.CheckID == checkID {
			return v, true
		}
	}
	return health.CheckVerdict{}, false
}

func TestXidErrors_IgnoredCode_Pass(t *testing.T) {
	// Xid 63 (memory remapping event) has immediate action IGNORE and is
	// not in any bucket, so it must not fire.
	nv := evalXid(t, 63)
	if nv.OverallSeverity != health.Pass {
		t.Errorf("ignored code 63: OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
}

func TestXidErrors_NoEvents_Pass(t *testing.T) {
	nv := evalXid(t)
	if nv.OverallSeverity != health.Pass {
		t.Errorf("clean boot: OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
}

func TestXidErrors_WorstSeverityWins(t *testing.T) {
	// An app fault (WARNING) alongside a reset-class fault (CRITICAL)
	// aggregates to CRITICAL.
	nv := evalXid(t, 11, 79)
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
}

func TestXidErrors_PerCodeCriticalCheck(t *testing.T) {
	// Xid 79 fires its own gpu_xid_79 check (CRITICAL, reset+escalate); every
	// other Xid check runs against the same measurement but passes.
	nv := evalXid(t, 79)

	var critical []health.CheckVerdict
	for _, v := range nv.CheckResults {
		if v.Severity == health.Critical {
			critical = append(critical, v)
		}
	}
	if len(critical) != 1 {
		t.Fatalf("got %d CRITICAL verdicts, want 1 (only gpu_xid_79)", len(critical))
	}
	if critical[0].CheckID != "gpu_xid_79" {
		t.Errorf("CheckID = %q, want gpu_xid_79", critical[0].CheckID)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !critical[0].RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", critical[0].RecommendedAction, want)
	}
}

func TestXidErrors_CHINonDataError_ResetsGPU(t *testing.T) {
	v, ok := checkResult(evalXid(t, 159), "gpu_xid_159")
	if !ok {
		t.Fatalf("gpu_xid_159 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
	want := "GPU Xid 159 detected: CHI non-data error. NVIDIA's CHECK_UVM guidance is to reset the GPU if UVM or vGPU is in use; otherwise ignore the error. Health Validation cannot determine whether either was in use when this error occurred, so it recommends a GPU reset as a conservative default."
	if len(v.Messages) != 1 || v.Messages[0] != want {
		t.Errorf("Messages = %q, want [%q]", v.Messages, want)
	}
}
