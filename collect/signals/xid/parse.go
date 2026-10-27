// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"regexp"
	"strconv"
	"strings"
)

// Parse error reasons. They are stable; ParseError.Detail wording is not.
const (
	ReasonUnrecognizedFormat = "unrecognized_format"
	ReasonInvalidCode        = "invalid_code"
	ReasonMissingPCIAddress  = "missing_pci_address"
)

var reasonDetails = map[string]string{
	ReasonUnrecognizedFormat: "the line has no parenthesized device group",
	ReasonMissingPCIAddress:  "the device group holds no PCI address",
	ReasonInvalidCode:        "the Xid code is missing or does not fit int64",
}

// ParseError reports an Xid line that matched the prefilter but did not
// parse. Detail never contains line content.
type ParseError struct {
	Reason string
	Detail string
}

func (e *ParseError) Error() string { return "xid parse: " + e.Reason + ": " + e.Detail }

// Event is the raw facts of one Xid line. An empty ProcessName means the
// driver attributed no process.
type Event struct {
	NVLink5        *NVLink5
	RecoveryAction *RecoveryAction
	PCIAddress     string
	ProcessName    string
	Code           int64
	PID            int64
	HasPID         bool
}

// NVLink5 is the extended detail of an NVLink5-form Xid 144-150 line.
// Register fields are raw hex text, empty when the line has no tail;
// DebugData holds the remaining tail words joined by one space.
type NVLink5 struct {
	SubComponent string
	IntrInfo     string
	ErrorStatus  string
	DebugData    string
	Link         int64
	Fatal        bool
}

// RecoveryAction is an Xid 154 recovery-action transition as printed.
type RecoveryAction struct {
	FromCode string
	FromName string
	ToCode   string
	ToName   string
}

const prefix = "NVRM: Xid "

var (
	// lineRE requires the driver's device group after the prefix. A process
	// name (at most 15 bytes) can put "NVRM: Xid " in a kernel line, for
	// example an OOM kill report, but cannot also supply the address.
	lineRE = regexp.MustCompile(`NVRM: Xid \((?:PCI:)?[0-9a-fA-F]{4,}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}`)
	// MIG GPU and compute instance attribution follows the address.
	deviceRE   = regexp.MustCompile(`^(?:PCI:)?([0-9a-fA-F:.]+)(?:\s+GPU-I:\d+)?(?:\s+GPU-CI:\d+)?$`)
	codeRE     = regexp.MustCompile(`^: (\d+)`)
	pidRE      = regexp.MustCompile(`^,\s*pid=([^,]*)`)
	nameRE     = regexp.MustCompile(`^,\s*name=([^,]*)`)
	digitsRE   = regexp.MustCompile(`^\d+$`)
	nvlink5RE  = regexp.MustCompile(`^,\s*(\S+)\s+(Fatal|Nonfatal)\s+XC\d+\s+i\d+\s+Link\s+(-?\d+)(?:\s+\(([^)]*)\))?`)
	recoveryRE = regexp.MustCompile(`GPU recovery action changed from (0x[0-9a-fA-F]+) \(([^)]*)\) to (0x[0-9a-fA-F]+) \(([^)]*)\)`)
)

// Match reports whether message is an NVRM Xid line: NVIDIA's "NVRM: Xid"
// marker followed by a device address. NVSwitch SXid lines do not match.
func Match(message string) bool { return lineRE.MatchString(message) }

// Parse extracts the raw facts from an NVRM Xid line. It returns a
// *ParseError when message matches [Match] but cannot be parsed.
func Parse(message string) (Event, error) {
	at := lineRE.FindStringIndex(message)
	if at == nil {
		return Event{}, parseError(ReasonUnrecognizedFormat)
	}
	rest := message[at[0]+len(prefix):]
	device, rest, ok := strings.Cut(rest[1:], ")")
	if !ok {
		return Event{}, parseError(ReasonUnrecognizedFormat)
	}
	address := deviceRE.FindStringSubmatch(device)
	if address == nil {
		return Event{}, parseError(ReasonMissingPCIAddress)
	}
	codeMatch := codeRE.FindStringSubmatch(rest)
	if codeMatch == nil {
		return Event{}, parseError(ReasonInvalidCode)
	}
	code, err := strconv.ParseInt(codeMatch[1], 10, 64)
	if err != nil {
		return Event{}, parseError(ReasonInvalidCode)
	}
	event := Event{PCIAddress: address[1], Code: code}
	rest = rest[len(codeMatch[0]):]

	if code == 154 {
		if m := recoveryRE.FindStringSubmatch(rest); m != nil {
			event.RecoveryAction = &RecoveryAction{FromCode: m[1], FromName: m[2], ToCode: m[3], ToName: m[4]}
		}
	}
	if m := pidRE.FindStringSubmatch(rest); m != nil {
		// A pid that is not a number (pid='<unknown>') ends parsing, so the
		// name= and NVLink5 fields after it, Fatal included, are not read.
		if !digitsRE.MatchString(m[1]) {
			return event, nil
		}
		if pid, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			event.PID, event.HasPID = pid, true
		}
		rest = rest[len(m[0]):]
	}
	if m := nameRE.FindStringSubmatch(rest); m != nil {
		if name := strings.TrimSpace(m[1]); name != "<unknown>" {
			event.ProcessName = name
		}
		rest = rest[len(m[0]):]
	}
	if code >= 144 && code <= 150 {
		if m := nvlink5RE.FindStringSubmatch(rest); m != nil {
			link, _ := strconv.ParseInt(m[3], 10, 64)
			detail := &NVLink5{SubComponent: m[1], Fatal: m[2] == "Fatal", Link: link}
			tail := strings.Fields(m[4])
			if len(tail) > 0 {
				detail.IntrInfo = tail[0]
			}
			if len(tail) > 1 {
				detail.ErrorStatus = tail[1]
			}
			if len(tail) > 2 {
				detail.DebugData = strings.Join(tail[2:], " ")
			}
			event.NVLink5 = detail
		}
	}
	return event, nil
}

func parseError(reason string) *ParseError {
	return &ParseError{Reason: reason, Detail: reasonDetails[reason]}
}
