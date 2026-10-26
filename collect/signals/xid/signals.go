// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package xid owns the canonical signals for parsed NVRM Xid events and the
// line parser shared by journal and kmsg readers.
package xid

import "github.com/dsx-ai-factory/health-validation/collect"

// WindowBoot is the scan-window value for a read of the current boot.
const WindowBoot = "boot"

// ScanWindow marks a completed scan and names its window.
var ScanWindow = collect.NewSignal("xid.scan.window", "")

// Raw facts of one Xid event, one observation per field per event.
var (
	Code                = collect.NewSignal("xid.event.code", "1")
	PCIAddress          = collect.NewSignal("xid.event.pci_address", "")
	PID                 = collect.NewSignal("xid.event.pid", "1")
	ProcessName         = collect.NewSignal("xid.event.process_name", "")
	GPC                 = collect.NewSignal("xid.event.gpc", "1")
	TPC                 = collect.NewSignal("xid.event.tpc", "1")
	SM                  = collect.NewSignal("xid.event.sm", "1")
	NVLink5SubComponent = collect.NewSignal("xid.event.nvlink5.sub_component", "")
	NVLink5Fatal        = collect.NewSignal("xid.event.nvlink5.fatal", "")
	NVLink5Link         = collect.NewSignal("xid.event.nvlink5.link", "1")
	NVLink5IntrInfo     = collect.NewSignal("xid.event.nvlink5.intr_info", "")
	NVLink5ErrorStatus  = collect.NewSignal("xid.event.nvlink5.error_status", "")
	NVLink5DebugData    = collect.NewSignal("xid.event.nvlink5.debug_data", "")
)

// The Xid 154 recovery-action transition. Codes stay as printed because
// driver releases renamed the actions (R610 renamed 0x1 and 0x2), so
// consumers key on the code.
var (
	RecoveryActionFromCode = collect.NewSignal("xid.event.recovery_action.from_code", "")
	RecoveryActionFromName = collect.NewSignal("xid.event.recovery_action.from_name", "")
	RecoveryActionToCode   = collect.NewSignal("xid.event.recovery_action.to_code", "")
	RecoveryActionToName   = collect.NewSignal("xid.event.recovery_action.to_name", "")
)

// All returns the scan marker followed by every event signal.
func All() collect.Signals {
	return append(collect.Signals{ScanWindow}, EventSignals()...)
}

// EventSignals returns the per-event signals.
func EventSignals() collect.Signals {
	return collect.Signals{
		Code, PCIAddress, PID, ProcessName, GPC, TPC, SM,
		NVLink5SubComponent, NVLink5Fatal, NVLink5Link, NVLink5IntrInfo, NVLink5ErrorStatus, NVLink5DebugData,
		RecoveryActionFromCode, RecoveryActionFromName, RecoveryActionToCode, RecoveryActionToName,
	}
}
