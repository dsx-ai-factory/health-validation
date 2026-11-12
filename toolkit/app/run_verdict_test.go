// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"maps"
	"slices"
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

// fixtureResult is one non-PASS check result a fixture run must report.
type fixtureResult struct {
	check    string
	entities []string
	action   health.RecommendedAction
	severity health.Severity
}

type fixtureCase struct {
	// skips lists the skips beyond architecture ones, by check ID.
	skips map[string]health.SkipReason
	name  string
	// want lists every non-PASS result; every other result must pass.
	want []fixtureResult
	node nodeFixture
	exit int
}

func fixtureCases() []fixtureCase {
	with := func(f nodeFixture, change func(*nodeFixture)) nodeFixture {
		change(&f)
		return f
	}
	xid := func(lines ...string) nodeFixture {
		return with(healthyGB200(), func(f *nodeFixture) { f.xid = lines })
	}
	hardware := health.GenericDrain()
	collection := health.Drain(health.RemediationActionCheckCollection)
	perGPU := func(check string, gpus ...string) []fixtureResult {
		out := make([]fixtureResult, 0, len(gpus))
		for _, g := range gpus {
			out = append(out, fixtureResult{check: check, severity: health.Critical, action: hardware, entities: []string{g}})
		}
		return out
	}
	return []fixtureCase{
		{name: "healthy GB200", node: healthyGB200(), exit: exitOK},
		{
			name: "continuous cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "CONTINUOUS"} }), exit: exitOK,
			skips: map[string]health.SkipReason{"gpu_compute_apps": health.CadenceMismatch, "gpu_power_limit": health.CadenceMismatch, "gpu_sensor_throttle": health.CadenceMismatch},
		},
		{name: "prolog cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "PROLOG"} }), exit: exitOK},
		{name: "maintenance cadence", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--cadence", "MAINTENANCE"} }), exit: exitOK},
		{
			name: "operator infra", node: with(healthyGB200(), func(f *nodeFixture) { f.args = []string{"--execution-context", "OPERATOR_INFRA"} }), exit: exitOK,
			skips: map[string]health.SkipReason{"gpu_compute_apps": health.DeploymentUnsupported},
		},
		{
			name: "override and disabled check", node: with(healthyGB200(), func(f *nodeFixture) {
				f.config = "checks:\n  gpu_power_limit:\n    expected_power_limit_w: 1000\ndisabled_checks:\n  - gpu_power_state\n"
			}), exit: exitUnhealthy,
			want:  perGPU("gpu_power_limit", "GPU-0", "GPU-1", "GPU-2", "GPU-3"),
			skips: map[string]health.SkipReason{"gpu_power_state": health.CheckDisabled},
		},
		{name: "Xid 79", node: xid(xid79Line), exit: exitUnhealthy, want: []fixtureResult{xid79}},
		{name: "Xid 48", node: xid(xid48Line), exit: exitUnhealthy, want: []fixtureResult{xid48}},
		{name: "Xid 79 and 48", node: xid(xid79Line, xid48Line), exit: exitUnhealthy, want: []fixtureResult{xid48, xid79}},
		{name: "Xid 149 fatal", node: xid(xid149Line), exit: exitUnhealthy, want: []fixtureResult{
			{check: "gpu_xid_149", severity: health.Critical, action: resetGPU, entities: []string{"0018:01:00"}},
			{check: "gpu_xid_149_4", severity: health.Critical, action: resetGPU, entities: []string{"0018:01:00"}},
		}},
		{name: "Xid 145 nonfatal", node: xid(xid145Line), exit: exitOK, want: []fixtureResult{
			{check: "gpu_xid_145", severity: health.Warning, action: health.NoAction, entities: []string{"0000:5f:00"}},
		}},
		{name: "Xid 154", node: xid(xid154Line), exit: exitUnhealthy, want: []fixtureResult{
			{check: "gpu_xid_154", severity: health.Critical, action: resetGPU, entities: []string{"0000:01:00"}},
		}},
		{name: "unhealthy GPUs and CPUs", node: unhealthyGB200(), exit: exitUnhealthy, want: slices.Concat(
			[]fixtureResult{
				{check: "cpu_count", severity: health.Critical, action: hardware},
				{check: "gpu_compute_apps", severity: health.Critical, action: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired), entities: []string{"GPU-3"}},
			},
			perGPU("gpu_power_limit", "GPU-3"),
			perGPU("gpu_power_state", "GPU-2"),
			perGPU("gpu_sensor_throttle", "GPU-0", "GPU-1", "GPU-2"),
			[]fixtureResult{{check: "numa_node_count", severity: health.Critical, action: hardware}},
		)},
		{name: "missing GPU", node: healthyNode("NVIDIA GB200", 3, 1200, 34, 53125), exit: exitUnhealthy, want: []fixtureResult{
			{check: "gpu_count", severity: health.Critical, action: hardware},
		}},
		{name: "one GPU's pstate unreadable", node: with(healthyGB200(), func(f *nodeFixture) { f.gpus[2].pstateFails = true }), exit: exitUnhealthy, want: []fixtureResult{
			{check: "gpu_power_state", severity: health.Unknown, action: collection, entities: []string{"GPU-2"}},
		}},
		{name: "inactive NVLink", node: with(healthyGB200(), func(f *nodeFixture) { f.gpus[1].inactive = 1 }), exit: exitUnhealthy, want: perGPU("nvlink_active", "GPU-1")},
		{name: "fatal link flap", node: with(healthyGB200(), func(f *nodeFixture) { f.linkFlaps = []string{linkDown, linkUp} }), exit: exitUnhealthy, want: []fixtureResult{
			{check: "rdma_linkflap_fatal", severity: health.Critical, action: hardware},
		}},
		{name: "link flap below the outage minimum", node: with(healthyGB200(), func(f *nodeFixture) {
			f.linkFlaps = []string{linkDown, linkUp}
			f.config = "checks:\n  rdma_linkflap_fatal:\n    min_outage_seconds: 120\n"
		}), exit: exitOK},
		{name: "healthy H100", node: healthyNode("NVIDIA H100 80GB HBM3", 8, 700, 1, 26562), exit: exitOK},
	}
}

var (
	resetGPU = health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	xid48    = fixtureResult{check: "gpu_xid_48", severity: health.Critical, action: resetGPU, entities: []string{"0000:01:00"}}
	xid79    = fixtureResult{check: "gpu_xid_79", severity: health.Critical, action: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired), entities: []string{"0000:01:00"}}
)

// Each fixture node reports exactly its expected non-PASS results, passes
// every other check it runs, and skips only checks its architecture, cadence,
// execution context, or config rules out. A collection regression therefore
// fails here as an unexpected result or a SENSOR_MISSING skip, not only as a
// changed exit code.
func TestRunFixtureVerdicts(t *testing.T) {
	for _, tc := range fixtureCases() {
		t.Run(tc.name, func(t *testing.T) {
			v, code := runFixture(t, tc.node)
			if code != tc.exit {
				t.Errorf("exit = %d, want %d", code, tc.exit)
			}
			var got []fixtureResult
			for _, r := range v.CheckResults {
				if r.Severity != health.Pass {
					got = append(got, fixtureResult{check: r.CheckID, severity: r.Severity, action: r.RecommendedAction, entities: r.AffectedEntities})
				}
			}
			key := func(r fixtureResult) string { return r.check + " " + strings.Join(r.entities, ",") }
			slices.SortFunc(got, func(a, b fixtureResult) int { return strings.Compare(key(a), key(b)) })
			want := slices.Clone(tc.want)
			slices.SortFunc(want, func(a, b fixtureResult) int { return strings.Compare(key(a), key(b)) })
			equal := slices.EqualFunc(got, want, func(a, b fixtureResult) bool {
				return a.check == b.check && a.severity == b.severity && a.action.Equal(b.action) && slices.Equal(a.entities, b.entities)
			})
			if !equal {
				t.Errorf("non-PASS results:\n got %+v\nwant %+v", got, want)
			}
			skipped := make(map[string]health.SkipReason)
			for _, s := range v.SkippedChecks {
				if s.SkipReason != health.ArchitectureUnsupported {
					skipped[s.CheckID] = s.SkipReason
				}
			}
			if !maps.Equal(skipped, tc.skips) {
				t.Errorf("skips = %v, want %v", skipped, tc.skips)
			}
		})
	}
}

// v0.3.2's Xid parser dropped these lines, so their checks passed. The
// collect parser reads the MIG and PCI-less forms as Xids, and fails the domain
// closed on a line it cannot parse.
func TestRunXidLinesV032Dropped(t *testing.T) {
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
