// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

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

// recoveryStep is what the recovery actions an Xid 154 can request call
// for. codes are the driver's NV2080_CTRL_GPU_RECOVERY_ACTION values,
// which NVML's nvmlDeviceGpuRecoveryAction_t numbers identically.
type recoveryStep struct {
	codes     []uint32
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
// not reported as needing only a reset. None (0x0) and the codes no step
// lists, such as Recover IMEX Domain (0x5), have rules of their own.
var recoverySteps = []recoveryStep{
	// Bus Reset (0x6) maps to a reboot, the one available step sure to
	// cover a bus reset.
	{
		codes:     []uint32{0x2, 0x6, 0x7}, // OS Reboot, Bus Reset, System Reboot
		requested: "an OS reboot",
		severity:  health.Critical,
		action:    health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),
	},
	{
		codes:     []uint32{0x1}, // GPU Reset (PF FLR)
		requested: "a GPU reset",
		severity:  health.Critical,
		action:    resetAndService(),
	},
	// Drain and Reset ends in the same reset, but unaffected work may run
	// to completion first, so it ranks below an immediate reset.
	{
		codes:     []uint32{0x4}, // Drain and Reset
		requested: "a drain and reset",
		guidance:  "Schedule no new work on the GPU; let unaffected jobs finish or reach a checkpoint, then reset the GPU to regain its full memory capacity.",
		severity:  health.Critical,
		action:    resetAndService(),
	},
	// NVIDIA's guidance is to quiesce P2P, query the action again, and
	// reset only if Drain P2P is still requested. The driver often clears
	// it without logging another Xid 154, so the journal cannot show the
	// re-query's answer; the sequence resets after quiescing, and the
	// message says when the reset can be skipped.
	{
		codes:     []uint32{0x3}, // Drain P2P
		requested: "that peer-to-peer traffic be stopped",
		guidance:  "Disable new scheduling, stop the processes doing P2P traffic and disable UVM persistence mode, then reset the GPU. If a query of the GPU's recovery action no longer reports Drain P2P after quiescing, the reset can be skipped.",
		severity:  health.Warning,
		action:    health.Drain(health.RemediationActionStopP2PTraffic, health.RemediationActionResetGPU, health.RemediationActionServiceRequired),
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
	// own severity-differentiated checks below, built by nvlink5Check.
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
// code-level Fatal/Nonfatal check, so every event on that code still
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
// differently, is left to [nvlink5Check]'s code-level Fatal/Nonfatal
// check.
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

// infoXids lists the codes NVIDIA's public Xid catalog
// (https://docs.nvidia.com/deploy/xid-errors/latest/analyzing-xid-catalog.html)
// marks Unused, gives the IGNORE resolution, or gives no resolution. They
// have no check of their own, and are known, not unknown. A code the catalog
// later gives an action stays informational until it is removed from here.
var infoXids = []int64{
	1, 2, 3, 4, 5, 6, 7, 9, 10, 12, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24,
	26, 27, 28, 29, 30, 33, 34, 35, 36, 37, 38, 42, 43, 44, 47, 49, 50, 51, 52,
	53, 55, 56, 57, 58, 59, 61, 63, 65, 66, 67, 73, 81, 87, 90, 91, 92, 93,
	106, 107, 108, 111, 112, 113, 114, 115, 116, 117, 118, 121, 122, 123, 124,
	125, 137, 138, 141, 152, 153, 157, 160, 161,
	162, 163, 164, 165, 166, 167, 168, 169, 170, 171, 172,
}

// Checks returns every Xid check shipped with the catalog: one CRITICAL
// check per critical Xid code, one Fatal/Nonfatal check per NVLink5
// (144-150) code, one additional per-SubXidCode check for each fault
// subtype NVIDIA's decode table differentiates that way (see
// [nvlink5SubXidCodeCheck]), one architecture-scoped check per warning Xid,
// a check for the recovery action Xid 154 reports (see [xid154Check]),
// [informationalCheck], which reports the codes NVIDIA gives no
// resolution, and [unknownCheck], which reports codes no other check covers.
//
// Every check consumes an [XidMeasurement] produced by the journal Xid
// sensor and tests its xid_counts map. Codes whose immediate action is
// IGNORE/empty, and Unused codes, have no check of their own and are
// reported together by [informationalCheck].
func Checks() []health.CheckDefinition {
	warningCount := 0
	for _, class := range warningClasses {
		warningCount += len(class.codes)
	}
	checks := make([]health.CheckDefinition, 0, len(criticalXids)+len(nvlink5Xids)+len(nvlink5SubXidCodes)+warningCount+4)
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
	checks = append(checks, xid45Check())
	checks = append(checks, xid154Check())
	checks = append(checks, informationalCheck())
	checks = append(checks, unknownCheck())
	// Every check reports its own events, whatever signals its builder set.
	for i := range checks {
		if !slices.Contains(checks[i].RequiredSignals, SignalEvents) {
			checks[i].RequiredSignals = append(checks[i].RequiredSignals, SignalEvents)
		}
	}
	return checks
}

// criticalCheck builds the per-code CRITICAL check for one Xid.
func criticalCheck(c criticalXid) health.CheckDefinition {
	def := baseCheck(
		codeCheckID(c.code),
		fmt.Sprintf("GPU Xid %d (%s)", c.code, c.description),
		fmt.Sprintf("Detects NVRM Xid %d in the system journal within the collection window.", c.code),
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
// (144-150). NVRM marks each occurrence Fatal or Nonfatal in the log
// line itself (see [toolkit/xid.ParseXid] / [NVLink5Detail]); the check
// mirrors that split: Fatal is CRITICAL, Nonfatal is WARNING with no
// recommended action (matching NVIDIA's own guidance that most
// non-fatal NVLink5 subcodes need no remediation — confirmed by
// [nvlink5DecodeTable]: Non-fatal-severity rows resolve to IGNORE in
// the overwhelming majority of cases).
//
// The Fatal rule uses [resetAndService]: filtering
// [nvlink5DecodeTable] to Fatal-severity rows only, ~95% (37 of 39)
// resolve to a GPU reset, then service if the reset doesn't clear it:
// exactly this sequence. The rare exceptions that resolve to IGNORE
// despite Fatal severity aren't distinguishable from a static per-check
// action (they depend on the specific IntrInfo subcode, only available
// per-event), so this errs toward the safer over-remediation rather
// than silently doing nothing.
//
// Unlike [criticalCheck], the check-level default here is
// [health.NoAction] and the Fatal rule sets its own RecommendedAction:
// a Rule can only override a non-zero check default (see
// [health.Rule.HasRecommendedAction]), so the zero-value "no action"
// outcome has to be the fallback, not an override.
func nvlink5Check(c nvlink5Xid) health.CheckDefinition {
	def := baseCheck(
		codeCheckID(c.code),
		fmt.Sprintf("GPU Xid %d (%s)", c.code, c.description),
		fmt.Sprintf("Detects NVRM Xid %d in the system journal within the collection window, differentiated by the Fatal/Nonfatal severity NVRM assigns each occurrence.", c.code),
		health.NoAction,
	)
	def.RequiredSignals = []string{SignalGPUID, SignalPCIAddress, SignalXidCounts, SignalFatalXidCounts}
	def.Thresholds = map[string]health.ThresholdSpec{
		"fatal_occurrences": {
			Value:       int64(1),
			ValueType:   health.ThresholdInt64,
			Category:    health.DefaultThreshold,
			Description: "Fatal occurrences on one GPU within the collection window that trigger the CRITICAL rule.",
			Unit:        "count",
		},
		"nonfatal_occurrences": {
			Value:       int64(1),
			ValueType:   health.ThresholdInt64,
			Category:    health.DefaultThreshold,
			Description: "Nonfatal occurrences on one GPU within the collection window that trigger the WARNING rule.",
			Unit:        "count",
		},
	}
	// NVLink5 (144-150) is Blackwell-only per NVIDIA's Xid catalog.
	def.Architectures = slices.Clone(blackwellArchitectures)
	def.Rules = []health.Rule{
		{
			Expression:        fmt.Sprintf("%[1]d in fatal_xid_counts && fatal_xid_counts[%[1]d] >= fatal_occurrences", c.code),
			Severity:          health.Critical,
			Message:           fmt.Sprintf("GPU Xid %d detected: %s (Fatal).", c.code, c.description),
			RecommendedAction: resetAndService(),
		},
		{
			// xid_counts includes Fatal occurrences, so a bare membership
			// test would report a lone Fatal occurrence as Nonfatal too.
			Expression: fmt.Sprintf("%[1]d in xid_counts && xid_counts[%[1]d] > (%[1]d in fatal_xid_counts ? fatal_xid_counts[%[1]d] : 0) && xid_counts[%[1]d] - (%[1]d in fatal_xid_counts ? fatal_xid_counts[%[1]d] : 0) >= nonfatal_occurrences", c.code),
			Severity:   health.Warning,
			Message:    fmt.Sprintf("GPU Xid %d detected: %s (Nonfatal).", c.code, c.description),
		},
	}
	return def
}

// nvlink5SubXidCodeCheck builds the check for one [nvlink5SubXidCode],
// for whichever Xid code and subcode it names.
func nvlink5SubXidCodeCheck(c nvlink5SubXidCode) health.CheckDefinition {
	action := health.NoAction
	severity := health.Warning
	if c.critical {
		action = resetAndService()
		severity = health.Critical
	}
	def := baseCheck(
		subXidCodeCheckID(c.subXidCode),
		fmt.Sprintf("GPU Xid %s (%s)", c.subXidCode, c.subcode),
		fmt.Sprintf("Detects NVRM Xid %s (%s) occurrences in the system journal within the collection window.", c.subXidCode, c.subcode),
		action,
	)
	def.RequiredSignals = []string{SignalGPUID, SignalPCIAddress, SignalSubXidCodeCounts}
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
		codeCheckID(code),
		fmt.Sprintf("GPU Xid %d Warning", code),
		fmt.Sprintf("Detects warning-class NVRM Xid %d in the system journal within the collection window.", code),
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
//   - Xid 45 is the only Xid within the collection window: WARNING, recommending restart_app.
//   - Another Xid is present: INFO with no action; that Xid's own check
//     carries the remediation.
//
// Xid 154 is not counted as another Xid because it only annotates one.
func xid45Check() health.CheckDefinition {
	def := baseCheck(
		"gpu_xid_45",
		"GPU Xid 45 (preemptive removal)",
		"Detects NVRM Xid 45 in the system journal within the collection window; informational when another Xid is present.",
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
		codeCheckID(154),
		"GPU Xid 154 (GPU recovery action changed)",
		"Detects NVRM Xid 154 in the system journal within the collection window and reports the recovery action the driver last requested for each GPU.",
		health.NoAction,
	)
	def.Architectures = slices.Clone(allArchitectures)
	def.RequiredSignals = []string{SignalGPUID, SignalPCIAddress, SignalRecoveryActionCounts}
	occurrences := def.Thresholds["occurrences"]
	occurrences.Description = "Xid 154 reports asking for one recovery action as a GPU's current action, within the collection window, that cause that action's rule to fire."
	def.Thresholds["occurrences"] = occurrences

	codeKey := func(code uint32) string { return strconv.Quote(fmt.Sprintf("0x%x", code)) }
	mapped := []string{codeKey(0x0)}
	for _, step := range recoverySteps {
		for _, code := range step.codes {
			mapped = append(mapped, codeKey(code))
		}
	}

	// Each rule fires once the reports asking for its action reach
	// occurrences, counted per action rather than across every Xid 154.
	reached := func(key string) string {
		return fmt.Sprintf("%[1]s in recovery_action_counts && recovery_action_counts[%[1]s] >= occurrences", key)
	}

	var rules []health.Rule
	for _, step := range recoverySteps {
		terms := make([]string, len(step.codes))
		for i, code := range step.codes {
			terms[i] = "(" + reached(codeKey(code)) + ")"
		}
		rules = append(rules, health.Rule{
			Expression:        strings.Join(terms, " || "),
			Severity:          step.severity,
			Message:           step.message(fmt.Sprintf("GPU Xid 154: the driver requested %s for at least one GPU.", step.requested)),
			RecommendedAction: step.action,
		})
	}
	def.Rules = append(rules,
		health.Rule{
			Expression:        fmt.Sprintf("recovery_action_counts.exists(k, !(k in [%s]) && recovery_action_counts[k] >= occurrences)", strings.Join(append(mapped, strconv.Quote(recoveryActionUnreadable)), ", ")),
			Severity:          health.Warning,
			Message:           "GPU Xid 154: the driver requested a recovery action this catalog does not recognize for at least one GPU. Monitor, investigate the printed transition and the accompanying Xid, and report the action code.",
			RecommendedAction: health.Monitor,
		},
		health.Rule{
			Expression:        reached(strconv.Quote(recoveryActionUnreadable)),
			Severity:          health.Warning,
			Message:           "GPU Xid 154: the recovery action for at least one GPU could not be read from its log line; inspect the Xid 154 line in the system journal.",
			RecommendedAction: health.Monitor,
		},
		health.Rule{
			Expression: reached(codeKey(0x0)),
			Severity:   health.Info,
			Message:    "GPU Xid 154: the driver requested no recovery action (None) for at least one GPU. Correlate with the accompanying Xid.",
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

		RequiredSignals: []string{SignalGPUID, SignalPCIAddress, SignalXidCounts},
		Thresholds: map[string]health.ThresholdSpec{
			"occurrences": {
				Value:       int64(1),
				ValueType:   health.ThresholdInt64,
				Category:    health.DefaultThreshold,
				Description: "Number of matching Xid occurrences on one GPU within the collection window that causes the check to fire.",
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

// codeCheckID is the ID of the check on one Xid code.
func codeCheckID(code int64) string { return fmt.Sprintf("gpu_xid_%d", code) }

// subXidCodeCheckID is the ID of the check on one NVLink5 fault subtype,
// such as gpu_xid_149_4 for "149.4".
func subXidCodeCheckID(subXidCode string) string {
	return "gpu_xid_" + strings.ToLower(strings.Replace(subXidCode, ".", "_", 1))
}

const (
	// unknownCheckID is the check that reports Xid codes no other check covers.
	unknownCheckID = "gpu_xid_unknown"
	// informationalCheckID is the check that reports the infoXids no other check covers.
	informationalCheckID = "gpu_xid_informational"
)

// codeSet is a set of Xid codes.
type codeSet map[int64]bool

// infoSet is infoXids as a set.
var infoSet = func() codeSet {
	set := codeSet{}
	for _, code := range infoXids {
		set[code] = true
	}
	return set
}()

// builtinCovered is the codes some check in [checkScopes] reports on. A
// sub-code check adds nothing, because its code has a check of its own.
var builtinCovered = sync.OnceValue(func() codeSet {
	covered := codeSet{}
	for _, s := range checkScopes {
		if s.sub == "" {
			covered[s.code] = true
		}
	}
	return covered
})

// coverage is the Xid codes some check covers: the built-in checks', and
// those a consumer's own checks cover (see [Accumulator.CoveredCodes]).
type coverage map[string][]int64

func (c coverage) covers(code int64) bool {
	if builtinCovered()[code] {
		return true
	}
	for _, codes := range c {
		if slices.Contains(codes, code) {
			return true
		}
	}
	return false
}

// isUnknown reports whether no check covers code and the catalog does not list it.
func (c coverage) isUnknown(code int64) bool { return !c.covers(code) && !infoSet[code] }

// isInformational reports whether code is an infoXids code no check covers.
func (c coverage) isInformational(code int64) bool { return infoSet[code] && !c.covers(code) }

// countsWhere is xid_counts restricted to the codes keep accepts. It is never
// nil, like the other derived signals.
func (m XidMeasurement) countsWhere(keep func(int64) bool) map[int64]int64 {
	out := make(map[int64]int64)
	for code, n := range m.XidCounts {
		if keep(code) {
			out[code] = n
		}
	}
	return out
}

// catchAllCheck builds a check that fires when any code in the derived
// signal reaches occurrences. It applies to every architecture, because the
// codes it reports have no architecture to scope them to.
func catchAllCheck(id, name, description, signal string, severity health.Severity, message string, action health.RecommendedAction) health.CheckDefinition {
	def := baseCheck(id, name, description, action)
	def.RequiredSignals = []string{SignalGPUID, SignalPCIAddress, signal}
	def.Architectures = slices.Clone(allArchitectures)
	def.Rules = []health.Rule{{
		Expression: fmt.Sprintf("%[1]s.exists(c, %[1]s[c] >= occurrences)", signal),
		Severity:   severity,
		Message:    message,
	}}
	return def
}

// unknownCheck reports codes no other check covers.
func unknownCheck() health.CheckDefinition {
	return catchAllCheck(
		unknownCheckID,
		"GPU Xid not described by the catalog",
		"Detects NVRM Xid codes in the system journal that no Health Validation check covers and that are not informational.",
		SignalUnknownXidCounts,
		health.Warning,
		"A GPU Xid code that Health Validation has no check for was logged, so it recommends no action.",
		health.Monitor,
	)
}

// informationalCheck reports the infoXids no other check covers. NVIDIA's
// catalog gives these codes no resolution, so the node takes no action.
func informationalCheck() health.CheckDefinition {
	return catchAllCheck(
		informationalCheckID,
		"GPU Xid with no resolution in the catalog",
		"Detects NVRM Xid codes in the system journal that the NVIDIA Xid catalog marks Unused, IGNORE or gives no resolution, and that no Health Validation check covers.",
		SignalInformationalXidCounts,
		health.Info,
		"A GPU Xid code that NVIDIA's Xid catalog gives no resolution was logged. No action is needed. The driver message is in events.",
		health.NoAction,
	)
}
