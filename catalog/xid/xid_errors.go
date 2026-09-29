package xid

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// criticalXid is one Xid code whose immediate-action resolution bucket
// (from the NVIDIA Xid catalog) is reset/hardware/RMA-class. Each such
// code ships as its own CRITICAL check (gpu_xid_<code>) so a verdict
// names exactly which fault fired and carries that bucket as the
// recommended action.
type criticalXid struct {
	description string
	action      health.RecommendedAction
	code        int64
}

// resetAndContactSupport is the remediation sequence for a Xid code
// whose immediate-action bucket is a GPU-only reset: attempt the reset,
// then escalate directly to support rather than the generic
// [health.GenericDrain] ladder's intermediate host-restart step.
func resetAndContactSupport() health.RecommendedAction {
	return health.Drain(health.RemediationActionResetGPU, health.RemediationActionContactSupport)
}

// criticalXids lists every critical Xid code, ordered by code. action
// is the immediate-action bucket; codes whose action is IGNORE/empty or
// that are marked Unused are excluded entirely.
var criticalXids = []criticalXid{
	{code: 46, action: resetAndContactSupport(), description: "GPU stopped processing"},
	{code: 48, action: resetAndContactSupport(), description: "double bit ECC error"},
	{code: 62, action: resetAndContactSupport(), description: "internal micro-controller halt"},
	{code: 64, action: resetAndContactSupport(), description: "GPU memory remapping failure"},
	{code: 79, action: resetAndContactSupport(), description: "GPU has fallen off the bus"},
	{code: 95, action: resetAndContactSupport(), description: "uncontained memory error"},
	{code: 109, action: resetAndContactSupport(), description: "context switch timeout error"},
	{code: 110, action: resetAndContactSupport(), description: "security fault error"},
	{code: 119, action: resetAndContactSupport(), description: "GSP RPC timeout"},
	{code: 120, action: resetAndContactSupport(), description: "GSP error"},
	{code: 140, action: resetAndContactSupport(), description: "ECC unrecovered error"},
	{code: 142, action: health.Drain(health.RemediationActionContactSupport), description: "NVENC3 error"},
	{code: 143, action: resetAndContactSupport(), description: "GPU initialization error"},
	// 144-150 (NVLink5/NETIR family) are not listed here: they get their
	// own severity-differentiated checks below, built by nvlink5Check.
	{code: 155, action: resetAndContactSupport(), description: "NVLink SW-defined error"},
	{code: 156, action: resetAndContactSupport(), description: "resource retirement event"},
	{code: 158, action: resetAndContactSupport(), description: "GPU fatal timeout"},
	{code: 159, action: health.Drain(health.RemediationActionCheckUVM), description: "CHI non-data error"},
}

// nvlink5Xid is one Xid code in the NVLink5/NVSwitch fault family
// (144-150, Blackwell-only). Unlike other Xid codes, NVRM itself marks
// each occurrence Fatal or Nonfatal in the log line, so the check
// differentiates severity and action on that basis instead of firing
// CRITICAL uniformly — see [nvlink5Check].
type nvlink5Xid struct {
	description string
	code        int64
}

var nvlink5Xids = []nvlink5Xid{
	{code: 144, description: "NVLink SAW error"},
	{code: 145, description: "NVLink RLW error"},
	{code: 146, description: "NVLink TLW error"},
	{code: 147, description: "NVLink TREX error"},
	{code: 148, description: "NVLink NVLPW_CTRL error"},
	{code: 149, description: "NVLink NETIR error"},
	{code: 150, description: "NVLink MSE error"},
}

// warningClass groups warning-severity Xid codes that share a
// resolution bucket. All warning classes live as separate rules in the
// single consolidated gpu_xid_warning check, each carrying its bucket
// as the recommended action.
type warningClass struct {
	message string
	codes   []int64
	action  health.RecommendedAction
}

var warningClasses = []warningClass{
	{
		action:  health.Drain(health.RemediationActionRestartApp),
		message: "Application-level GPU Xid error detected. Restart the faulting application; the GPU itself remains healthy.",
		codes: []int64{
			8, 11, 13, 25, 31, 32, 39, 40, 41, 60, 68, 69, 70, 71, 72, 75, 76, 77,
			82, 83, 84, 85, 86, 88, 89, 94, 96, 97, 98, 99, 100, 101, 102, 103,
			104, 105, 126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 139,
		},
	},
	{
		action:  health.Drain(health.RemediationActionWorkflowXid45),
		message: "GPU Xid 45 (preemptive removal) detected. Follow the Xid 45 workflow.",
		codes:   []int64{45},
	},
	{
		action:  health.Drain(health.RemediationActionUpdateSWFW),
		message: "GPU Xid 78 (vGPU start error) detected. Update GPU software/firmware.",
		codes:   []int64{78},
	},
	{
		action:  health.Drain(health.RemediationActionRestartOS),
		message: "GPU Xid 151 (key rotation error) detected. Restart the affected VM or host.",
		codes:   []int64{151},
	},
}

// Checks returns every Xid check shipped with the catalog: one CRITICAL
// check per critical Xid code, a single consolidated WARNING check
// covering all warning-severity codes, and an INFO check for Xid 154
// (a recovery-action notice that only annotates another Xid).
//
// Every check consumes an [XidMeasurement] produced by the journal Xid
// sensor and tests its xid_counts map. Codes whose immediate action is
// IGNORE/empty, and Unused codes, are not represented and never fire.
func Checks() []health.CheckDefinition {
	checks := make([]health.CheckDefinition, 0, len(criticalXids)+len(nvlink5Xids)+2)
	for _, c := range criticalXids {
		checks = append(checks, criticalCheck(c))
	}
	for _, c := range nvlink5Xids {
		checks = append(checks, nvlink5Check(c))
	}
	checks = append(checks, warningCheck(), xid154Check())
	return checks
}

// criticalCheck builds the per-code CRITICAL check for one Xid.
func criticalCheck(c criticalXid) health.CheckDefinition {
	def := baseCheck(
		fmt.Sprintf("gpu_xid_%d", c.code),
		fmt.Sprintf("GPU Xid %d (%s)", c.code, c.description),
		fmt.Sprintf("Detects NVRM Xid %d in the system journal since boot.", c.code),
		c.action,
	)
	def.Rules = []health.Rule{{
		Expression: fmt.Sprintf("%d in xid_counts", c.code),
		Severity:   health.Critical,
		Message:    fmt.Sprintf("GPU Xid %d detected: %s.", c.code, c.description),
	}}
	return def
}

// nvlink5Check builds the per-code check for one NVLink5-family Xid
// (144-150). NVRM marks each occurrence Fatal or Nonfatal in the log
// line itself (see [toolkit/xid.ParseXid] / [NVLink5Detail]); the check
// mirrors that split: Fatal is CRITICAL, Nonfatal is WARNING with no
// recommended action (matching NVIDIA's own guidance that most
// non-fatal NVLink5 subcodes need no remediation — confirmed against
// NVIDIA's Xid 144-150 decode table: Non-fatal-severity rows resolve to
// IGNORE in the overwhelming majority of cases).
//
// The Fatal rule uses [resetAndContactSupport]: filtering NVIDIA's Xid
// 144-150 decode table to Fatal-severity rows only, ~95% (37 of 39)
// resolve to a GPU reset with support contacted if it doesn't clear —
// exactly this sequence. The rare exceptions that resolve to IGNORE
// despite Fatal severity aren't distinguishable from a static per-check
// action (they depend on the specific IntrInfo fault variant, only
// available per-event), so this errs toward the safer over-remediation
// rather than silently doing nothing.
//
// Unlike [criticalCheck], the check-level default here is
// [health.NoAction] and the Fatal rule sets its own RecommendedAction:
// a Rule can only override a non-zero check default (see
// [health.Rule.HasRecommendedAction]), so the zero-value "no action"
// outcome has to be the fallback, not an override.
func nvlink5Check(c nvlink5Xid) health.CheckDefinition {
	def := baseCheck(
		fmt.Sprintf("gpu_xid_%d", c.code),
		fmt.Sprintf("GPU Xid %d (%s)", c.code, c.description),
		fmt.Sprintf("Detects NVRM Xid %d in the system journal since boot, differentiated by the Fatal/Nonfatal severity NVRM assigns each occurrence.", c.code),
		health.NoAction,
	)
	def.RequiredSignals = []string{SignalXidCounts, SignalFatalXidCounts}
	// Pinned rather than inherited. This currently matches baseCheck, but
	// for a different reason: baseCheck is GB200/GB300 because H100/A100
	// support is deprioritized, while NVLink5 (144-150) is Blackwell-only
	// per NVIDIA's Xid catalog and does not exist on those parts at all.
	// If baseCheck's coverage ever widens again, this must not follow it.
	def.Architectures = []string{"GB200", "GB300"}
	def.Rules = []health.Rule{
		{
			Expression:        fmt.Sprintf("%d in fatal_xid_counts", c.code),
			Severity:          health.Critical,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s (Fatal).", c.code, c.description),
			RecommendedAction: resetAndContactSupport(),
		},
		{
			Expression: fmt.Sprintf("%d in xid_counts", c.code),
			Severity:   health.Warning,
			Message:    fmt.Sprintf("GPU Xid %d detected: %s (Nonfatal).", c.code, c.description),
		},
	}
	return def
}

// warningCheck builds the single consolidated WARNING check, one rule
// per warning resolution bucket.
func warningCheck() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_warning",
		"GPU Xid Warnings",
		"Detects application-level and otherwise recoverable NVRM Xid errors in the system journal since boot, grouped by their NVIDIA Xid-catalog immediate-action resolution bucket.",
		health.Drain(health.RemediationActionRestartApp),
	)
	rules := make([]health.Rule, 0, len(warningClasses))
	for _, w := range warningClasses {
		rules = append(rules, health.Rule{
			Expression:        xidMatchExpr(w.codes),
			Severity:          health.Warning,
			Message:           w.message,
			RecommendedAction: w.action,
		})
	}
	def.Rules = rules
	return def
}

// xid154Check builds the INFO check for Xid 154.
func xid154Check() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_154",
		"GPU Xid 154 (GPU recovery action changed)",
		"Detects NVRM Xid 154 in the system journal since boot. Informational; correlates with an accompanying Xid.",
		health.Drain(health.RemediationActionXid154),
	)
	def.Rules = []health.Rule{{
		Expression: "154 in xid_counts",
		Severity:   health.Info,
		Message:    "GPU Xid 154 (GPU recovery action changed) observed. Informational; correlate with the accompanying Xid.",
	}}
	return def
}

// baseCheck returns a CheckDefinition with the fields every Xid check
// shares; callers set Rules.
func baseCheck(id, name, description string, recommendedAction health.RecommendedAction) health.CheckDefinition {
	return health.CheckDefinition{
		ID:          id,
		Name:        name,
		Description: description,
		Category:    health.Xid,

		RequiredSignals: []string{SignalXidCounts},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: recommendedAction,
	}
}

// xidMatchExpr builds a CEL expression that is true when the xid_counts
// map contains any of the given Xid codes, e.g.
//
//	xid_counts.exists(k, k in [8, 11, 25])
func xidMatchExpr(codes []int64) string {
	strs := make([]string, len(codes))
	for i, c := range codes {
		strs[i] = strconv.FormatInt(c, 10)
	}
	return fmt.Sprintf("xid_counts.exists(k, k in [%s])", strings.Join(strs, ", "))
}
