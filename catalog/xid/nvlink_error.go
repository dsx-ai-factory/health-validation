// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// NVIDIA's WORKFLOW_NVLINK_ERR describes seven DWORDs per link, with Hopper
// status in registers 1, 3, 4 and 5.
var (
	nvlinkErrorPrefixRE = regexp.MustCompile(`(?i)\bNVLINK:`)
	nvlinkLinkTupleRE   = regexp.MustCompile(`(?i)\blink\s+\d+\s*\(([^()]*)\)`)
	// nvlinkLinkWordRE finds every "link" after the prefix, so a header
	// that is truncated ("link 4") or malformed ("link x(") is not skipped.
	nvlinkLinkWordRE = regexp.MustCompile(`(?i)\blink\b`)
	// nvlinkRegisterSeparatorRE splits a tuple's registers on commas,
	// semicolons or whitespace, as different drivers print them.
	nvlinkRegisterSeparatorRE = regexp.MustCompile(`\s*[,;]\s*|\s+`)
)

// Keys in nvlink_error_counts that are not register groups.
const (
	nvlinkKeyTotal      = "total"
	nvlinkKeyUnreadable = "unreadable"
	nvlinkKeyUnmapped   = "unmapped"
	// Suffix for a deferring group seen with other error bits in one report.
	nvlinkAccompaniedSuffix = "_accompanied"
)

// nvlinkErrorGroup is one set of Xid 74 register bits that share a meaning.
type nvlinkErrorGroup struct {
	// key names the group in nvlink_error_counts.
	key string
	// label names a deferring group in its accompanied message.
	label   string
	message string
	// masks holds one bit mask per register; index 0 is the first register.
	masks [7]uint32
	// benign bits need no action.
	benign bool
	// monitor bits are reported as WARNING with monitor rather than drained.
	monitor bool
	// defers to nonbenign, nondeferring groups in the same report.
	defers bool
}

// nvlinkErrorGroups lists the Hopper Xid 74 register bits.
var nvlinkErrorGroups = []nvlinkErrorGroup{
	{
		key:     "fabric_reset",
		masks:   [7]uint32{3: 1 << 18},
		message: "NVLink fabric reset required. Reboot the node to reset the NVLink fabric; contact support if the error persists.",
	},
	{
		key:     "ignored",
		benign:  true,
		masks:   [7]uint32{0: 1<<0 | 1<<23 | 1<<30},
		message: "NVLink status bits that need no action.",
	},
	{
		key:     "secondary",
		label:   "Secondary-error",
		defers:  true,
		masks:   [7]uint32{0: 1<<1 | 1<<20, 3: 1<<16 | 1<<17},
		message: "Secondary NVLink error without accompanying fabric-reset, ECC/parity, link-hardware, repeat-class or unexpected bits in the report. Contact support if it persists.",
	},
	{
		key: "ecc",
		masks: [7]uint32{
			0: 1<<4 | 1<<5,
			2: 1<<0 | 1<<1 | 1<<2 | 1<<6,
			4: 1<<18 | 1<<19 | 1<<21 | 1<<22 | 1<<24 | 1<<25 | 1<<27 | 1<<28,
		},
		message: "NVLink ECC/parity error. Contact support if it recurs on the same link.",
	},
	{
		key:     "mechanical",
		masks:   [7]uint32{0: 1<<8 | 1<<9 | 1<<12 | 1<<16 | 1<<17 | 1<<24 | 1<<28},
		message: "Possible NVLink hardware issue. Check the link connections; contact support if it persists.",
	},
	{
		key:     "signal_integrity",
		label:   "Signal-integrity",
		defers:  true,
		masks:   [7]uint32{0: 1<<21 | 1<<22},
		message: "Marginal NVLink signal integrity without accompanying fabric-reset, ECC/parity, link-hardware, repeat-class or unexpected bits in the report. Check the link connections; contact support if it persists.",
	},
	{
		key:     "recurring",
		monitor: true,
		masks:   [7]uint32{0: 1<<27 | 1<<29, 2: 1<<16 | 1<<17 | 1<<18 | 1<<19},
		message: "NVLink error detected. Contact support if it occurs repeatedly on the same link.",
	},
	{
		key:     "unexpected",
		masks:   [7]uint32{2: 1 << 13},
		message: "NVLink error bit not expected in production. Contact support.",
	},
	{
		key:     "corrected",
		benign:  true,
		masks:   [7]uint32{4: 1<<20 | 1<<23 | 1<<26 | 1<<29},
		message: "Corrected NVLink ECC errors exceeded a threshold; no uncorrectable error occurred.",
	},
}

// nvlinkKnownBits has a bit set wherever a group gives the register bit a meaning.
var nvlinkKnownBits = func() [7]uint32 {
	var known [7]uint32
	for _, group := range nvlinkErrorGroups {
		for i, mask := range group.masks {
			known[i] |= mask
		}
	}
	return known
}()

// nvlinkErrorRegisters returns the seven registers of each link in an Xid 74
// message, or false if any link in the message cannot be read.
func nvlinkErrorRegisters(message string) ([][7]uint32, bool) {
	prefix := nvlinkErrorPrefixRE.FindStringIndex(message)
	if prefix == nil {
		return nil, false
	}
	rest := message[prefix[1]:]
	matches := nvlinkLinkTupleRE.FindAllStringSubmatch(rest, -1)
	if len(matches) == 0 || len(matches) != len(nvlinkLinkWordRE.FindAllStringIndex(rest, -1)) {
		return nil, false
	}
	out := make([][7]uint32, 0, len(matches))
	for _, match := range matches {
		var registers [7]uint32
		fields := nvlinkRegisterSeparatorRE.Split(strings.TrimSpace(match[1]), -1)
		if len(fields) != len(registers) {
			return nil, false
		}
		for i, field := range fields {
			if !strings.HasPrefix(strings.ToLower(field), "0x") {
				return nil, false
			}
			value, ok := parseHexUint32(field)
			if !ok {
				return nil, false
			}
			registers[i] = value
		}
		out = append(out, registers)
	}
	return out, true
}

// nvlinkErrorCounts counts the Xid 74 reports that match each key, once per
// report however many of its bits or links match.
func (m XidMeasurement) nvlinkErrorCounts() map[string]int64 {
	counts := map[string]int64{}
	var events int64
	for _, event := range m.Events {
		if event.XidCode != 74 {
			continue
		}
		events++
		links, ok := nvlinkErrorRegisters(event.Message)
		if !ok {
			counts[nvlinkKeyUnreadable]++
			continue
		}
		matched := map[string]bool{}
		hit := map[string]bool{}
		for _, registers := range links {
			var populated uint32
			unknown := false
			for i, value := range registers {
				populated |= value
				unknown = unknown || value&^nvlinkKnownBits[i] != 0
			}
			if unknown || populated == 0 {
				matched[nvlinkKeyUnmapped] = true
			}
			for _, group := range nvlinkErrorGroups {
				for i, mask := range group.masks {
					if registers[i]&mask != 0 {
						hit[group.key] = true
					}
				}
			}
		}
		// Benign and deferring groups cannot explain another deferring group.
		accompanied := false
		for _, group := range nvlinkErrorGroups {
			accompanied = accompanied || hit[group.key] && !group.benign && !group.defers
		}
		for _, group := range nvlinkErrorGroups {
			switch {
			case !hit[group.key]:
			case group.defers && accompanied:
				matched[group.key+nvlinkAccompaniedSuffix] = true
			default:
				matched[group.key] = true
			}
		}
		for key := range matched {
			counts[key]++
		}
	}
	// Counts-only consumers must not silently pass an undecodable event.
	if missing := m.XidCounts[74] - events; missing > 0 {
		counts[nvlinkKeyUnreadable] += missing
		events += missing
	}
	if events > 0 {
		counts[nvlinkKeyTotal] = events
	}
	return counts
}

// xid74Check returns the check for Xid 74 (NVLink error). On Hopper it
// decodes the NVLink error registers in the driver message; on A100 it
// reports every Xid 74.
func xid74Check() health.CheckDefinition {
	def := baseCheck(codeCheckID(74), "GPU Xid 74 (NVLink error)",
		"Detects NVRM Xid 74 in the system journal within the collection window; on H100 and H200, decodes the NVLink error registers to set severity and recommended action.", health.NoAction)
	def.Architectures = slices.Clone(ampereHopperArchitectures)
	def.RequiredSignals = []string{SignalGPUID, SignalPCIAddress, SignalXidCounts, SignalNVLinkErrorCounts}
	def.Thresholds["decode_hopper_registers"] = health.ThresholdSpec{
		Value:           false,
		ValueType:       health.ThresholdBool,
		Category:        health.FixedThreshold,
		Description:     "Whether the architecture uses the Hopper Xid 74 register layout. Fixed per architecture.",
		PerArchitecture: map[string]any{"H100": true, "H200": true, "A100": false},
	}
	occurrences := def.Thresholds["occurrences"]
	occurrences.Description = "Number of matching Xid 74 reports on one GPU within the collection window that triggers a rule. H100/H200 count each classification separately, including accompanied and unaccompanied secondary/signal-integrity bits; reports in different classifications do not add together. Each report counts at most once per classification, regardless of matching bits or links. A100 counts all Xid 74 reports together."
	def.Thresholds["occurrences"] = occurrences
	reached := func(key string) string {
		return fmt.Sprintf("%[1]q in nvlink_error_counts && nvlink_error_counts[%[1]q] >= occurrences", key)
	}
	def.Rules = []health.Rule{{
		Expression:        "!decode_hopper_registers && (" + reached(nvlinkKeyTotal) + ")",
		Severity:          health.Critical,
		Message:           "GPU Xid 74 detected: NVLink connection error. Reset the GPU or reboot the node to clear the error. Inspect nvidia-smi nvlink output and the peer device; contact support if recovery fails.",
		RecommendedAction: health.GenericDrain(),
	}}
	add := func(key, message string, severity health.Severity, action health.RecommendedAction) {
		def.Rules = append(def.Rules, health.Rule{
			Expression:        "decode_hopper_registers && (" + reached(key) + ")",
			Severity:          severity,
			Message:           "GPU Xid 74: " + message,
			RecommendedAction: action,
		})
	}
	for _, group := range nvlinkErrorGroups {
		severity, action := health.Critical, health.GenericDrain()
		switch {
		case group.benign:
			severity, action = health.Info, health.NoAction
		case group.monitor:
			severity, action = health.Warning, health.Monitor
		case group.key == "fabric_reset":
			action = health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
		}
		add(group.key, group.message, severity, action)
		if group.defers {
			add(group.key+nvlinkAccompaniedSuffix, group.label+" bits accompanied other documented error bits in the report; follow the guidance for those bits.", health.Info, health.NoAction)
		}
	}
	add(nvlinkKeyUnmapped, "NVLink register dump with undocumented bits or no status bits set. Inspect the log line and contact support.", health.Warning, health.Monitor)
	add(nvlinkKeyUnreadable, "NVLink link number or registers could not be read. Inspect the log line and contact support.", health.Warning, health.Monitor)
	return def
}
