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

	linkDown = "mlx5_core 0000:5a:00.0 enp90s0np0: Link down"
	linkUp   = "mlx5_core 0000:5a:00.0 enp90s0np0: Link up"
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
		{name: "Xid 154", node: xid(xid154Line), exit: exitUnhealthy},
		{name: "unhealthy GPUs and CPUs", node: unhealthyGB200(), exit: exitUnhealthy},
		{name: "missing GPU", node: healthyNode("NVIDIA GB200", 3, 1200, 34, 53125), exit: exitUnhealthy},
		{name: "one GPU's pstate unreadable", node: with(healthyGB200(), func(f *nodeFixture) { f.gpus[2].pstateFails = true }), exit: exitUnhealthy},
		{name: "inactive NVLink", node: with(healthyGB200(), func(f *nodeFixture) { f.gpus[1].inactive = 1 }), exit: exitUnhealthy},
		{name: "fatal link flap", node: with(healthyGB200(), func(f *nodeFixture) { f.linkFlaps = []string{linkDown, linkUp} }), exit: exitUnhealthy},
		{name: "link flap below the outage minimum", node: with(healthyGB200(), func(f *nodeFixture) {
			f.linkFlaps = []string{linkDown, linkUp}
			f.config = "checks:\n  rdma_linkflap_fatal:\n    min_outage_seconds: 120\n"
		}), exit: exitOK},
		{name: "healthy H100", node: healthyNode("NVIDIA H100 80GB HBM3", 8, 700, 1, 26562), exit: exitOK},
	}
}

// Each fixture node gives its expected exit code, and every failing Xid result
// names the GPU that logged it (#231).
func TestRunFixtureVerdicts(t *testing.T) {
	for _, tc := range parityCases() {
		t.Run(tc.name, func(t *testing.T) {
			v, code := runFixture(t, tc.node)
			if code != tc.exit {
				t.Errorf("exit = %d, want %d", code, tc.exit)
			}
			for _, r := range v.CheckResults {
				if r.Category == health.Xid && r.Severity != health.Pass && len(r.AffectedEntities) == 0 {
					t.Errorf("%s names no GPU", r.CheckID)
				}
			}
		})
	}
}

// The legacy Xid parser dropped these lines, so their checks passed. The
// collect parser reads the MIG and PCI-less forms as Xids, and fails the domain
// closed on a line it cannot parse.
func TestRunXidLinesLegacyDropped(t *testing.T) {
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
			collected, code := runFixture(t, f)
			if code != exitUnhealthy {
				t.Fatalf("exit = %d, want %d", code, exitUnhealthy)
			}
			failed := 0
			for _, r := range collected.CheckResults {
				if !strings.HasPrefix(r.CheckID, "gpu_xid_") || (tc.check != "" && r.CheckID != tc.check) {
					continue
				}
				failed++
				if r.Severity != tc.severity || !r.RecommendedAction.Equal(tc.action) {
					t.Errorf("%s = %s %+v, want %s %+v", r.CheckID, r.Severity, r.RecommendedAction, tc.severity, tc.action)
				}
			}
			if failed == 0 {
				t.Error("no matching Xid result")
			}
		})
	}
}

// A user who can read only their own journal sees no kernel entries. The Xid
// and link-flap checks must report that as a collection failure, not a clean
// boot (#204).
func TestRunKernelJournalUnreadable(t *testing.T) {
	f := healthyGB200()
	f.userJournalOnly = true
	v, code := runFixture(t, f)
	if code != exitUnhealthy {
		t.Errorf("exit = %d, want %d", code, exitUnhealthy)
	}
	journalChecks := 0
	for _, r := range v.CheckResults {
		if r.Category != health.Xid && r.Category != health.InfiniBand {
			continue
		}
		journalChecks++
		if r.Severity != health.Unknown {
			t.Errorf("%s = %s, want %s", r.CheckID, r.Severity, health.Unknown)
		}
	}
	if journalChecks == 0 {
		t.Error("no Xid or link-flap results")
	}
}
