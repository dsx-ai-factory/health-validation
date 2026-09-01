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

// criticalXids lists every critical Xid code, ordered by code. action
// is the immediate-action bucket; codes whose action is IGNORE/empty or
// that are marked Unused are excluded entirely.
var criticalXids = []criticalXid{
	{code: 46, action: health.Drain(health.DiagnosticActionResetGPU), description: "GPU stopped processing"},
	{code: 54, action: health.Drain(health.DiagnosticActionCheckMechanicals), description: "auxiliary power is not connected to the GPU board"},
	{code: 62, action: health.Drain(health.DiagnosticActionResetGPU), description: "internal micro-controller halt"},
	{code: 64, action: health.Drain(health.DiagnosticActionResetGPU), description: "GPU memory remapping failure"},
	{code: 74, action: health.Drain(health.DiagnosticActionWorkflowNVLinkErr), description: "NVLink error"},
	{code: 79, action: health.Drain(health.DiagnosticActionRestartBM), description: "GPU has fallen off the bus"},
	{code: 95, action: health.Drain(health.DiagnosticActionResetGPU), description: "uncontained memory error"},
	{code: 109, action: health.Drain(health.DiagnosticActionResetGPU), description: "context switch timeout error"},
	{code: 110, action: health.Drain(health.DiagnosticActionResetGPU), description: "security fault error"},
	{code: 119, action: health.Drain(health.DiagnosticActionResetGPU), description: "GSP RPC timeout"},
	{code: 120, action: health.Drain(health.DiagnosticActionResetGPU), description: "GSP error"},
	{code: 136, action: health.Drain(health.DiagnosticActionResetGPU), description: "link training failed"},
	{code: 140, action: health.Drain(health.DiagnosticActionResetGPU), description: "ECC unrecovered error"},
	{code: 142, action: health.Drain(health.DiagnosticActionContactSupport), description: "NVENC3 error"},
	{code: 143, action: health.Drain(health.DiagnosticActionResetGPU), description: "GPU initialization error"},
	{code: 144, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink SAW error"},
	{code: 145, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink RLW error"},
	{code: 146, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink TLW error"},
	{code: 147, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink TREX error"},
	{code: 148, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink NVLPW_CTRL error"},
	{code: 149, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink NETIR error"},
	{code: 150, action: health.Drain(health.DiagnosticActionWorkflowNVLink5Err), description: "NVLink MSE error"},
	{code: 155, action: health.Drain(health.DiagnosticActionResetGPU), description: "NVLink SW-defined error"},
	{code: 156, action: health.Drain(health.DiagnosticActionResetGPU), description: "resource retirement event"},
	{code: 158, action: health.Drain(health.DiagnosticActionResetGPU), description: "GPU fatal timeout"},
	{code: 159, action: health.Drain(health.DiagnosticActionCheckUVM), description: "CHI non-data error"},
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
		action:  health.Drain(health.DiagnosticActionRestartApp),
		message: "Application-level GPU Xid error detected. Restart the faulting application; the GPU itself remains healthy.",
		codes: []int64{
			8, 11, 25, 32, 39, 40, 41, 60, 68, 69, 70, 71, 72, 75, 76, 77,
			80, 82, 83, 84, 85, 86, 88, 89, 94, 96, 97, 98, 99, 100, 101, 102, 103,
			104, 105, 126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 139,
		},
	},
	{
		action:  health.Drain(health.DiagnosticActionWorkflowXid45),
		message: "GPU Xid 45 (preemptive removal) detected. Follow the Xid 45 workflow.",
		codes:   []int64{45},
	},
	{
		action:  health.Drain(health.DiagnosticActionUpdateSWFW),
		message: "GPU Xid 78 (vGPU start error) detected. Update GPU software/firmware.",
		codes:   []int64{78},
	},
	{
		action:  health.Drain(health.DiagnosticActionRestartVM),
		message: "GPU Xid 151 (key rotation error) detected. Restart the affected VM.",
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
	checks := make([]health.CheckDefinition, 0, len(criticalXids)+2)
	for _, c := range criticalXids {
		checks = append(checks, criticalCheck(c))
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

// warningCheck builds the single consolidated WARNING check, one rule
// per warning resolution bucket.
func warningCheck() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_warning",
		"GPU Xid Warnings",
		"Detects application-level and otherwise recoverable NVRM Xid errors in the system journal since boot, grouped by their NVIDIA Xid-catalog immediate-action resolution bucket.",
		health.Drain(health.DiagnosticActionRestartApp),
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
		health.Drain(health.DiagnosticActionXid154),
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
		Architectures:     []string{"GB200", "GB300", "H100", "A100"},

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
