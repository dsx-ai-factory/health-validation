// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// criticalXid is one Xid code whose immediate-action resolution bucket
// (from the NVIDIA Xid catalog) is reset/hardware/RMA/restart-VM-class.
// Each such code ships as its own CRITICAL check (gpu_xid_<code>) so a
// verdict names exactly which fault fired and carries that bucket as the
// recommended action.
type criticalXid struct {
	description   string
	message       string
	architectures []string
	action        health.RecommendedAction
	code          int64
}

var (
	allArchitectures             = []string{"GB200", "GB300", "B200", "H100", "H200", "A100"}
	blackwellArchitectures       = []string{"GB200", "GB300", "B200"}
	hopperBlackwellArchitectures = []string{"GB200", "GB300", "B200", "H100", "H200"}
	ampereHopperArchitectures    = []string{"H100", "H200", "A100"}
	hopperArchitectures          = []string{"H100", "H200"}
	// graceBlackwellArchitectures covers Xids NVIDIA's catalog marks
	// GraceBlackwell and do not occur on an HGX B200 baseboard.
	graceBlackwellArchitectures = []string{"GB200", "GB300"}
	hgxArchitectures            = []string{"B200", "H100", "H200", "A100"}
)

// resetAndService is the remediation sequence for a Xid code
// whose immediate-action bucket is a GPU-only reset: attempt the reset,
// then hand the node to the operator to be serviced, skipping the
// generic [health.GenericDrain] ladder's intermediate host-restart step.
func resetAndService() health.RecommendedAction {
	return health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
}

// recoveryStep is what one recovery action an Xid 154 can request calls
// for. The Xid 154 check and the NVLink5 checks that defer to it share
// these, so one driver request gets the same advice from both.
type recoveryStep struct {
	bucket    string
	requested string
	// guidance follows the request in a message when the action alone
	// does not say what to do first.
	guidance string
	action   health.RecommendedAction
	severity health.Severity
}

func (s recoveryStep) message(finding string) string {
	if s.guidance == "" {
		return finding
	}
	return finding + " " + s.guidance
}

// recoverySteps is ordered most disruptive first: rules that tie on
// severity take the first match's action, so a GPU needing a reboot is
// not reported as needing only a reset.
var recoverySteps = []recoveryStep{
	{
		bucket:    ResolutionRestartOS,
		requested: "an OS reboot",
		severity:  health.Critical,
		action:    health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),
	},
	{
		bucket:    ResolutionResetGPU,
		requested: "a GPU reset",
		severity:  health.Critical,
		action:    resetAndService(),
	},
	// Drain P2P is not a final verdict: the driver names the real
	// action once P2P traffic stops, logging another Xid 154. NVIDIA's
	// guidance is to reset the GPU if Drain P2P is still requested after
	// the drain, so it shares the reset's steps.
	{
		bucket:    ResolutionDrainP2P,
		requested: "that peer-to-peer traffic be stopped",
		guidance:  "Stop the processes using peer-to-peer and disable persistence mode, then re-run; reset the GPU if this is still requested.",
		severity:  health.Critical,
		action:    resetAndService(),
	},
}

// criticalXids lists every critical Xid code, ordered by code. action
// is the immediate-action bucket; codes whose action is IGNORE/empty or
// that are marked Unused are excluded entirely.
var criticalXids = []criticalXid{
	{code: 46, action: resetAndService(), architectures: allArchitectures, description: "GPU stopped processing"},
	{code: 48, action: resetAndService(), architectures: allArchitectures, description: "double bit ECC error"},
	{code: 54, action: health.Drain(health.RemediationActionServiceRequired), architectures: hgxArchitectures, description: "auxiliary power is not connected to the GPU board"},
	{code: 62, action: resetAndService(), architectures: allArchitectures, description: "internal micro-controller halt"},
	{code: 64, action: resetAndService(), architectures: allArchitectures, description: "GPU memory remapping failure"},
	{
		code:          79,
		action:        health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),
		architectures: allArchitectures,
		description:   "GPU has fallen off the bus",
		message:       "GPU Xid 79 detected: GPU has fallen off the bus and is inaccessible over PCIe. Restart the host. Review system and kernel PCI event logs; contact support for further investigation.",
	},
	{code: 95, action: resetAndService(), architectures: allArchitectures, description: "uncontained memory error"},
	{code: 109, action: resetAndService(), architectures: allArchitectures, description: "context switch timeout error"},
	{code: 110, action: resetAndService(), architectures: hopperBlackwellArchitectures, description: "security fault error"},
	{code: 119, action: resetAndService(), architectures: allArchitectures, description: "GSP RPC timeout"},
	{code: 120, action: resetAndService(), architectures: allArchitectures, description: "GSP error"},
	{code: 136, action: resetAndService(), architectures: hopperArchitectures, description: "link training failed"},
	{code: 140, action: resetAndService(), architectures: allArchitectures, description: "ECC unrecovered error"},
	{code: 142, action: health.Drain(health.RemediationActionServiceRequired), architectures: graceBlackwellArchitectures, description: "NVENC3 error"},
	{code: 143, action: resetAndService(), architectures: hopperBlackwellArchitectures, description: "GPU initialization error"},
	// 144-150 (NVLink5/NETIR family) are not listed here: they get their
	// own decode-table-driven checks below, built by nvlink5Check.
	{
		code:          151,
		action:        health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),
		architectures: hopperBlackwellArchitectures,
		description:   "key rotation error",
		message:       "GPU Xid 151 (key rotation error) detected. Restart the affected VM; contact the operator if the error persists.",
	},
	{code: 155, action: resetAndService(), architectures: blackwellArchitectures, description: "NVLink SW-defined error"},
	{code: 156, action: resetAndService(), architectures: hopperBlackwellArchitectures, description: "resource retirement event"},
	{code: 158, action: resetAndService(), architectures: allArchitectures, description: "GPU fatal timeout"},
	{
		code:          159,
		action:        resetAndService(),
		architectures: blackwellArchitectures,
		description:   "CHI non-data error",
		message:       "GPU Xid 159 detected: CHI non-data error. NVIDIA's CHECK_UVM guidance is to reset the GPU if UVM or vGPU is in use; otherwise ignore the error. Health Validation cannot determine whether either was in use when this error occurred, so it recommends a GPU reset as a conservative default.",
	},
}

// nvlink5Xid is one Xid code in the NVLink5/NVSwitch fault family
// (144-150, Blackwell-only). Unlike other Xid codes, NVIDIA publishes a
// decode table naming the required action per subcomponent and error
// status, so the check reports the resolved action rather than firing
// CRITICAL on every occurrence — see [nvlink5Check].
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

// nvlink5SubXidCode is one fault subtype NVIDIA's decode table
// differentiates purely by SubXidCode (IntrInfo[25:20]) rather than by
// ErrorStatus — e.g. Xid 149's NETIR_LINK_EVT/NETIR_LINK_DOWN family,
// the one operators configure per-subcode in practice (e.g. "149.4",
// "149.A"). Most (code, subcode) groups in [nvlink5DecodeTable] instead
// vary by ErrorStatus, where SubXidCode alone doesn't distinguish the
// different resolutions — see [nvlink5SubXidCodes] for which groups
// qualify.
//
// Each one gets its own check (gpu_xid_<subXidCode>, e.g.
// gpu_xid_149_4), additive to (not a replacement for) [nvlink5Check]'s
// code-level resolution check, so every event on that code still
// produces at least that baseline verdict. Splitting lets an operator
// independently disable/override the guidance for one specific fault
// subtype without touching the rest.
//
// The code-level check still fires on every event, so silencing one
// subtype entirely means disabling both checks.
type nvlink5SubXidCode struct {
	subXidCode    string // e.g. "149.4"
	subcode       string // e.g. "NETIR_LINK_EVT/NETIR_LINK_DOWN"
	investigatory string
	critical      bool // true when the decode table's resolution is RESET_GPU rather than IGNORE
}

// nvlink5SubXidCodes derives every [nvlink5SubXidCode] from
// [nvlink5DecodeTable]: rows within a (code, subcode) group that share
// a blank ErrorStatus and are disambiguated purely by their IntrInfo
// pattern's embedded SubXidCode digit (see [nvlink5SubXidCodeDigit]),
// keeping only digits every row under that code resolves alike (see
// [nvlink5SubXidCodeResolvesAlike]). A group with only one row, whose
// rows vary by ErrorStatus instead, or whose digit other rows resolve
// differently, is left to [nvlink5Check]'s code-level resolution check.
var nvlink5SubXidCodes = deriveNVLink5SubXidCodes()

// deriveNVLink5SubXidCodes builds [nvlink5SubXidCodes]' value; split
// out so the derivation logic isn't buried in a var initializer.
func deriveNVLink5SubXidCodes() []nvlink5SubXidCode {
	type groupKey struct {
		subcode string
		code    int64
	}
	var order []groupKey
	groups := make(map[groupKey][]nvlink5DecodeRow)
	for _, row := range nvlink5DecodeTable {
		if row.errorStatus != "" {
			continue // disambiguated by ErrorStatus, not SubXidCode
		}
		k := groupKey{row.subcode, row.code}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], row)
	}

	var out []nvlink5SubXidCode
	for _, k := range order {
		rows := groups[k]
		if len(rows) < 2 {
			continue // nothing to differentiate by SubXidCode
		}
		for _, row := range rows {
			digit, ok := nvlink5SubXidCodeDigit(row)
			if !ok {
				continue
			}
			if !nvlink5SubXidCodeResolvesAlike(row.code, digit, row.resolution) {
				continue
			}
			out = append(out, nvlink5SubXidCode{
				subXidCode:    fmt.Sprintf("%d.%s", row.code, digit),
				subcode:       row.subcode,
				investigatory: row.investigatory,
				critical:      row.resolution == "RESET_GPU",
			})
		}
	}
	return out
}

// nvlink5SubXidCodeResolvesAlike reports whether every row under code
// sharing this SubXidCode digit calls for the same resolution.
//
// sub_xid_code_counts is keyed from the Xid code and IntrInfo[25:20]
// (see [XidEvent.SubXidCode]), not from the decode-table row an event
// matches. Several rows under one code can therefore share a digit, and
// where they disagree the signal cannot say which of them occurred. Xid 149
// digit 0 covers NETIR_LINK_DOWN (RESET_GPU) and NETIR_BER_EVENT
// (IGNORE) alike, so a check keyed on it would recommend a GPU reset
// for a bit-error event NVIDIA says to ignore.
//
// Such a digit names no single fault subtype, which is the whole
// premise of a per-subcode check, so it gets no check and its events
// are left to [nvlink5Check]'s code-level verdict.
func nvlink5SubXidCodeResolvesAlike(code int64, digit, resolution string) bool {
	for _, row := range nvlink5DecodeTable {
		if row.code != code {
			continue
		}
		rowDigit, ok := nvlink5SubXidCodeDigit(row)
		if ok && rowDigit != digit {
			continue
		}
		// !ok means the row wildcards the digit bits and so matches
		// every digit under this code, including this one.
		if row.resolution != resolution {
			return false
		}
	}
	return true
}

// nvlink5SubXidCodeDigit extracts row's SubXidCode digit: the literal
// 6-bit value at characters 6-11 of its v1Pattern, the fixed IntrInfo
// bit position (see [nvlink5SubXidCodeMask]) both driver eras place it
// at — formatted as [XidEvent.SubXidCode] formats it, so the label
// matches what a check's CEL rule looks up in sub_xid_code_counts. Returns ok=false if that bit range isn't a fixed
// value in this row's pattern (wildcarded, meaning the row isn't
// SubXidCode-differentiated).
func nvlink5SubXidCodeDigit(row nvlink5DecodeRow) (string, bool) {
	if len(row.v1Pattern) != 32 {
		return "", false
	}
	bits := row.v1Pattern[6:12]
	if strings.ContainsRune(bits, '-') {
		return "", false
	}
	v, err := strconv.ParseUint(bits, 2, 8)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%X", v), true
}

// warningClass groups warning-severity Xid codes that share an architecture
// set and resolution bucket. Each code becomes its own stable check.
type warningClass struct {
	message       string
	architectures []string
	codes         []int64
	action        health.RecommendedAction
}

var warningClasses = []warningClass{
	{
		action:        health.Drain(health.RemediationActionRestartApp),
		architectures: allArchitectures,
		message:       "GPU Xid %d detected: application-level error. Restart the faulting application.",
		codes: []int64{
			8, 11, 13, 25, 31, 32, 39, 40, 41, 60, 68, 69, 70, 71, 72, 75, 76, 77,
			82, 83, 84, 85, 86, 88, 89, 94,
		},
	},
	{
		action:        health.Drain(health.RemediationActionUpdateSWFW),
		architectures: allArchitectures,
		message:       "GPU Xid %d (vGPU start error) detected: the guest driver is incompatible with the host vGPU Manager, or the vGPU type is not supported for the guest OS or GPU. Install a compatible guest driver, update the host vGPU Manager, or assign a supported vGPU type.",
		codes:         []int64{78},
	},
	{
		action:        health.Drain(health.RemediationActionRestartApp),
		architectures: ampereHopperArchitectures,
		message:       "GPU Xid %d detected: corrupted data sent to the GPU. Restart the faulting application.",
		codes:         []int64{80},
	},
	{
		action:        health.Drain(health.RemediationActionRestartApp),
		architectures: hopperBlackwellArchitectures,
		message:       "GPU Xid %d detected: application-level error. Restart the faulting application.",
		codes:         []int64{96, 97, 98, 99, 100, 101, 102, 103, 104, 105},
	},
	{
		action:        health.Drain(health.RemediationActionRestartApp),
		architectures: blackwellArchitectures,
		message:       "GPU Xid %d detected: application-level error. Restart the faulting application.",
		codes:         []int64{126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 139},
	},
}

// Checks returns every Xid check shipped with the catalog: one CRITICAL
// check per critical Xid code, one check per NVLink5 (144-150) code
// reporting what its occurrences resolve to (see [nvlink5Check]), one
// additional per-SubXidCode check for each fault subtype NVIDIA's decode
// table differentiates that way (see [nvlink5SubXidCodeCheck]), one
// architecture-scoped check per warning Xid, and a check for the recovery
// action Xid 154 reports (see [xid154Check]).
//
// Every check consumes an [XidMeasurement]. Codes whose immediate action
// is IGNORE/empty, and Unused codes, are not represented and never fire.
func Checks() []health.CheckDefinition {
	warningCount := 0
	for _, class := range warningClasses {
		warningCount += len(class.codes)
	}
	checks := make([]health.CheckDefinition, 0, len(criticalXids)+len(nvlink5Xids)+len(nvlink5SubXidCodes)+warningCount+2)
	for _, c := range criticalXids {
		checks = append(checks, criticalCheck(c))
	}
	for _, c := range nvlink5Xids {
		checks = append(checks, nvlink5Check(c))
	}
	for _, c := range nvlink5SubXidCodes {
		checks = append(checks, nvlink5SubXidCodeCheck(c))
	}
	for _, class := range warningClasses {
		for _, code := range class.codes {
			checks = append(checks, warningCheck(code, class))
		}
	}
	checks = append(checks, xid45Check(), xid154Check())
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
	def.Architectures = slices.Clone(c.architectures)
	message := c.message
	if message == "" {
		message = fmt.Sprintf("GPU Xid %d detected: %s.", c.code, c.description)
	}
	def.Rules = []health.Rule{{
		Expression: fmt.Sprintf("%[1]d in xid_counts && xid_counts[%[1]d] >= occurrences", c.code),
		Severity:   health.Critical,
		Message:    message,
	}}
	return def
}

// nvlink5Check builds the per-code check for one NVLink5-family Xid
// (144-150). It reports what each occurrence resolves to through
// NVIDIA's decode table (see [NVLink5ResolutionCounts]), not the
// Fatal/Nonfatal flag NVRM prints, which the table is more specific
// than: CRITICAL for a reset or reboot, WARNING where the action is
// unknown, INFO where the table calls for none.
//
// Rules that tie on severity take the first match's action, so within
// each severity the rules carrying an action come before those without,
// and service_required before restart_app.
//
// Unlike [criticalCheck], the check-level default here is
// [health.NoAction] and each rule sets its own RecommendedAction: a
// Rule can only override a non-zero check default (see
// [health.Rule.HasRecommendedAction]), so the zero-value "no action"
// outcome has to be the fallback, not an override.
func nvlink5Check(c nvlink5Xid) health.CheckDefinition {
	def := baseCheck(
		fmt.Sprintf("gpu_xid_%d", c.code),
		fmt.Sprintf("GPU Xid %d (%s)", c.code, c.description),
		fmt.Sprintf("Detects NVRM Xid %d and reports it by the immediate action NVIDIA's Xid 144-150 decode table calls for, resolving rows that defer to Xid 154 through the recovery action the driver reported for the same GPU.", c.code),
		health.NoAction,
	)
	def.RequiredSignals = []string{SignalNVLink5ResolutionCounts}
	// NVLink5 (144-150) is Blackwell-only per NVIDIA's Xid catalog.
	def.Architectures = slices.Clone(blackwellArchitectures)

	// Each rule fires once this code has resolved to its bucket at least
	// occurrences times (baseCheck's threshold).
	key := func(bucket string) string {
		return fmt.Sprintf("%[1]q in nvlink5_resolution_counts && nvlink5_resolution_counts[%[1]q] >= occurrences", NVLink5ResolutionKey(c.code, bucket))
	}
	// IGNORE reports INFO, not WARNING: roughly half the table's rows
	// resolve to it and NVIDIA's guidance is to take no action, so the
	// occurrence stays visible without affecting the node's disposition
	// (INFO exits 0). Escalating on the bare occurrence instead makes
	// reading the table pointless, because a node's severity is the worst
	// across its checks and the escalation would stand regardless.
	var rules []health.Rule
	for _, step := range recoverySteps {
		if step.bucket == ResolutionResetGPU {
			continue // also a decode-table action, with its own message below
		}
		rules = append(rules, health.Rule{
			Expression:        key(step.bucket),
			Severity:          step.severity,
			Message:           step.message(fmt.Sprintf("GPU Xid %d detected: %s. Its resolution defers to Xid 154, which requested %s for the affected GPU.", c.code, c.description, step.requested)),
			RecommendedAction: step.action,
		})
	}
	def.Rules = append(rules, []health.Rule{
		{
			Expression:        key(ResolutionResetGPU),
			Severity:          health.Critical,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s. NVIDIA's decode table resolves this occurrence to a GPU reset.", c.code, c.description),
			RecommendedAction: resetAndService(),
		},
		{
			Expression:        key(ResolutionResetGPUUnconfirmed),
			Severity:          health.Critical,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s. NVIDIA's decode table does not confirm a reset for this occurrence, either because it has no entry for this subcomponent and error status (NVRM marked it Fatal) or because the driver version needed to read its registers is unknown, so it is escalated conservatively.", c.code, c.description),
			RecommendedAction: resetAndService(),
		},
		{
			Expression:        key(ResolutionXid154Unrecognized),
			Severity:          health.Warning,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s. Its resolution defers to an Xid 154 for the affected GPU whose recovery action this catalog could not read or does not recognize; treat the fault as unresolved and report the Xid 154 line.", c.code, c.description),
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
		},
		{
			Expression:        key(ResolutionXid154Missing),
			Severity:          health.Warning,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s. Its resolution defers to Xid 154, and no Xid 154 was found for the affected GPU, which normally means the driver requested no recovery action. Restart the faulting application.", c.code, c.description),
			RecommendedAction: health.Drain(health.RemediationActionRestartApp),
		},
		{
			Expression: key(ResolutionUndecoded),
			Severity:   health.Warning,
			Message:    fmt.Sprintf("GPU Xid %d detected: %s. Its log line could not be decoded, so its severity and required action are unknown; inspect the line in the system journal.", c.code, c.description),
		},
		{
			Expression: key(ResolutionIgnore),
			Severity:   health.Info,
			Message:    fmt.Sprintf("GPU Xid %d detected: %s. This occurrence resolves to no action.", c.code, c.description),
		},
	}...)
	return def
}

// nvlink5SubXidCodeCheck builds the check for one [nvlink5SubXidCode],
// for whichever Xid code and subcode it names.
func nvlink5SubXidCodeCheck(c nvlink5SubXidCode) health.CheckDefinition {
	id := "gpu_xid_" + strings.ToLower(strings.Replace(c.subXidCode, ".", "_", 1))
	// A subtype the table resolves to IGNORE reports INFO, as the
	// code-level check does for IGNORE: visible, but not a node fault.
	action := health.NoAction
	severity := health.Info
	if c.critical {
		action = resetAndService()
		severity = health.Critical
	}
	def := baseCheck(
		id,
		fmt.Sprintf("GPU Xid %s (%s)", c.subXidCode, c.subcode),
		fmt.Sprintf("Detects NVRM Xid %s (%s) occurrences in the system journal since boot.", c.subXidCode, c.subcode),
		action,
	)
	def.RequiredSignals = []string{SignalSubXidCodeCounts}
	def.Architectures = slices.Clone(blackwellArchitectures)
	def.Rules = []health.Rule{{
		Expression: fmt.Sprintf("%[1]q in sub_xid_code_counts && sub_xid_code_counts[%[1]q] >= occurrences", c.subXidCode),
		Severity:   severity,
		Message:    fmt.Sprintf("GPU Xid %s (%s) detected. Investigatory guidance: %s.", c.subXidCode, c.subcode, c.investigatory),
	}}
	return def
}

// warningCheck builds one stable, architecture-scoped check for a warning Xid.
func warningCheck(code int64, class warningClass) health.CheckDefinition {
	def := baseCheck(
		fmt.Sprintf("gpu_xid_%d", code),
		fmt.Sprintf("GPU Xid %d Warning", code),
		fmt.Sprintf("Detects warning-class NVRM Xid %d in the system journal since boot.", code),
		class.action,
	)
	def.Architectures = slices.Clone(class.architectures)
	def.Rules = []health.Rule{{
		Expression: fmt.Sprintf("%[1]d in xid_counts && xid_counts[%[1]d] >= occurrences", code),
		Severity:   health.Warning,
		Message:    fmt.Sprintf(class.message, code),
	}}
	return def
}

// xid45Check builds the check for Xid 45 (preemptive removal), following
// NVIDIA's WORKFLOW_XID_45 resolution bucket:
//
//   - Xid 45 is the only Xid since boot: WARNING, recommending restart_app.
//   - Another Xid is present: INFO with no action; that Xid's own check
//     carries the remediation.
//
// Xid 154 is not counted as another Xid because it only annotates one.
func xid45Check() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_45",
		"GPU Xid 45 (preemptive removal)",
		"Detects NVRM Xid 45 in the system journal since boot; informational when another Xid is present.",
		health.NoAction,
	)
	def.Architectures = slices.Clone(allArchitectures)
	present := "45 in xid_counts && xid_counts[45] >= occurrences"
	companion := "xid_counts.exists(c, c != 45 && c != 154)"
	def.Rules = []health.Rule{
		{
			Expression: fmt.Sprintf("%s && %s", present, companion),
			Severity:   health.Info,
			Message:    "GPU Xid 45 accompanied another Xid; no Xid 45-specific remediation is needed. Follow the accompanying Xid's guidance.",
		},
		{
			Expression:        fmt.Sprintf("%s && !%s", present, companion),
			Severity:          health.Warning,
			Message:           "GPU Xid 45 occurred alone; restart the application. If Xid 45 recurs with no other Xid, report the issue.",
			RecommendedAction: health.Drain(health.RemediationActionRestartApp),
		},
	}
	return def
}

// xid154Check builds the check for Xid 154. It reads the recovery action
// each GPU was left in, so a request the driver made alongside any Xid,
// not only an NVLink5 fault that defers to it, reaches the operator.
func xid154Check() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_154",
		"GPU Xid 154 (GPU recovery action changed)",
		"Detects NVRM Xid 154 in the system journal since boot and reports the recovery action the driver last requested for each GPU.",
		health.NoAction,
	)
	def.Architectures = slices.Clone(allArchitectures)
	def.RequiredSignals = []string{SignalXidCounts, SignalRecoveryActionCounts}

	codesByBucket := make(map[string][]string)
	var mapped []string
	for code, bucket := range recoveryActionBuckets {
		key := strconv.Quote(fmt.Sprintf("0x%x", code))
		codesByBucket[bucket] = append(codesByBucket[bucket], key)
		mapped = append(mapped, key)
	}
	slices.Sort(mapped)

	// Every rule also needs occurrences Xid 154 lines, so raising the
	// threshold quiets the check as a whole.
	const seen = " && 154 in xid_counts && xid_counts[154] >= occurrences"

	var rules []health.Rule
	for _, step := range recoverySteps {
		codes := codesByBucket[step.bucket]
		slices.Sort(codes)
		terms := make([]string, len(codes))
		for i, code := range codes {
			terms[i] = code + " in recovery_action_counts"
		}
		rules = append(rules, health.Rule{
			Expression:        "(" + strings.Join(terms, " || ") + ")" + seen,
			Severity:          step.severity,
			Message:           step.message(fmt.Sprintf("GPU Xid 154: the driver requested %s for at least one GPU.", step.requested)),
			RecommendedAction: step.action,
		})
	}
	def.Rules = append(rules,
		health.Rule{
			Expression:        fmt.Sprintf("recovery_action_counts.exists(k, !(k in [%s]))", strings.Join(mapped, ", ")) + seen,
			Severity:          health.Warning,
			Message:           "GPU Xid 154: the driver requested a recovery action this catalog does not recognize for at least one GPU; report the action code.",
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
		},
		health.Rule{
			Expression: "154 in xid_counts && xid_counts[154] >= occurrences",
			Severity:   health.Info,
			Message:    "GPU Xid 154 (GPU recovery action changed) observed. Informational; correlate with the accompanying Xid.",
		},
	)
	return def
}

// baseCheck returns a Blackwell-scoped CheckDefinition with the fields
// every Xid check shares. Callers widen Architectures where applicable
// and set Rules.
func baseCheck(id, name, description string, recommendedAction health.RecommendedAction) health.CheckDefinition {
	return health.CheckDefinition{
		ID:          id,
		Name:        name,
		Description: description,
		Category:    health.Xid,

		RequiredSignals: []string{SignalXidCounts},
		Thresholds: map[string]health.ThresholdSpec{
			"occurrences": {
				Value:       int64(1),
				ValueType:   health.ThresholdInt64,
				Category:    health.DefaultThreshold,
				Description: "Number of matching Xid occurrences since boot that causes the check to fire.",
				Unit:        "count",
			},
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     slices.Clone(blackwellArchitectures),

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: recommendedAction,
	}
}
