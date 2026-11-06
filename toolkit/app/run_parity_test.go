// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

const (
	xid79Line  = "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus."
	xid48Line  = "NVRM: Xid (PCI:0000:01:00): 48, pid=1234, name=python3, An uncorrectable double bit error (DBE) has been detected on GPU in the framebuffer at partition 0, subpartition 0."
	xid149Line = "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)"
	xid145Line = "NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02"
	xid154Line = "NVRM: Xid (PCI:0000:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x1 (GPU Reset Required)"
)

type parityCase struct {
	name string
	node nodeFixture
	exit int
}

func parityCases() []parityCase {
	with := func(f nodeFixture, change func(*nodeFixture)) nodeFixture {
		change(&f)
		return f
	}
	xid := func(lines ...string) nodeFixture {
		return with(healthyGB200(), func(f *nodeFixture) { f.xid = lines })
	}
	return []parityCase{
		{name: "healthy GB200", node: healthyGB200(), exit: exitOK},
		{name: "continuous cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "CONTINUOUS"} }), exit: exitOK},
		{name: "prolog cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "PROLOG"} }), exit: exitOK},
		{name: "maintenance cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "MAINTENANCE"} }), exit: exitOK},
		{name: "operator infra", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--execution-context", "OPERATOR_INFRA"} }), exit: exitOK},
		{name: "override and disabled check", node: with(healthyGB200(), func(f *nodeFixture) {
			f.config = "checks:\n  gpu_power_limit:\n    expected_power_limit_w: 1000\ndisabled_checks:\n  - gpu_power_state\n"
		}), exit: exitUnhealthy},
		{name: "Xid 79", node: xid(xid79Line), exit: exitUnhealthy},
		{name: "Xid 48", node: xid(xid48Line), exit: exitUnhealthy},
		{name: "Xid 79 and 48", node: xid(xid79Line, xid48Line), exit: exitUnhealthy},
		{name: "Xid 149 fatal", node: xid(xid149Line), exit: exitUnhealthy},
		{name: "Xid 145 nonfatal", node: xid(xid145Line), exit: exitOK},
		{name: "Xid 154", node: xid(xid154Line), exit: exitOK},
		{name: "unhealthy GPUs and CPUs", node: unhealthyGB200(), exit: exitUnhealthy},
		{name: "missing GPU", node: healthyNode("NVIDIA GB200", 3, 1200, 34), exit: exitUnhealthy},
		{name: "one GPU's pstate unreadable", node: with(healthyGB200(), func(f *nodeFixture) { f.gpus[2].pstateFails = true }), exit: exitUnhealthy},
		{name: "healthy H100", node: healthyNode("NVIDIA H100 80GB HBM3", 8, 700, 1), exit: exitOK},
	}
}

// Legacy backends and collect sources fed the same node give the same verdict
// and exit code.
func TestRunCollectParity(t *testing.T) {
	for _, tc := range parityCases() {
		t.Run(tc.name, func(t *testing.T) {
			legacy, legacyExit := runFixture(t, tc.node, legacyPath)
			collected, collectExit := runFixture(t, tc.node, collectPath)
			if legacyExit != tc.exit || collectExit != tc.exit {
				t.Errorf("exit = %d legacy, %d collect, want %d", legacyExit, collectExit, tc.exit)
			}
			if diff := verdictDiff(legacy, collected); diff != "" {
				t.Errorf("legacy (-) and collect (+) verdicts differ at %s", diff)
			}
		})
	}
}

// The legacy Xid parser drops these lines, so their checks pass. The collect
// parser reads the MIG and PCI-less forms as Xids, and fails the domain closed
// on a line it cannot parse.
func TestRunCollectDocumentedDivergences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     string
		check    string
		action   health.RecommendedAction
		severity health.Severity
	}{
		{
			name: "unparseable Xid line", line: "NVRM: Xid (PCI:0000:01:00): x",
			action: health.Drain(health.RemediationActionCheckCollection), severity: health.Unknown,
		},
		{
			name: "MIG Xid line", line: "NVRM: Xid (PCI:0000:01:00 GPU-I:1 GPU-CI:0): 48, pid=1234, name=python3, An uncorrectable double bit error (DBE) has been detected on GPU in the framebuffer at partition 0, subpartition 0.",
			check: "gpu_xid_48", action: health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired), severity: health.Critical,
		},
		{
			name: "Xid line without a PCI prefix", line: "NVRM: Xid (0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus.",
			check: "gpu_xid_79", action: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired), severity: health.Critical,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthyGB200()
			f.xid = []string{tc.line}
			legacy, legacyExit := runFixture(t, f, legacyPath)
			collected, collectExit := runFixture(t, f, collectPath)
			if legacyExit != exitOK || collectExit != exitUnhealthy {
				t.Fatalf("exit = %d legacy, %d collect, want %d and %d", legacyExit, collectExit, exitOK, exitUnhealthy)
			}
			for _, r := range legacy.CheckResults {
				if strings.HasPrefix(r.CheckID, "gpu_xid_") && r.Severity != health.Pass {
					t.Errorf("legacy %s = %s, want PASS", r.CheckID, r.Severity)
				}
			}
			failed := 0
			for _, r := range collected.CheckResults {
				if !strings.HasPrefix(r.CheckID, "gpu_xid_") || (tc.check != "" && r.CheckID != tc.check) {
					continue
				}
				failed++
				if r.Severity != tc.severity || !r.RecommendedAction.Equal(tc.action) {
					t.Errorf("collect %s = %s %+v, want %s %+v", r.CheckID, r.Severity, r.RecommendedAction, tc.severity, tc.action)
				}
			}
			if failed == 0 {
				t.Error("collect has no matching Xid result")
			}
		})
	}
}
