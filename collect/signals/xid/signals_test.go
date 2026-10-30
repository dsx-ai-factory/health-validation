// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/stretchr/testify/require"
)

func TestSignals(t *testing.T) {
	tests := []struct {
		definition collect.Signal
		id         string
		unit       string
	}{
		{definition: ScanWindow, id: "xid.scan.window"},
		{definition: Code, id: "xid.event.code", unit: "1"},
		{definition: PCIAddress, id: "xid.event.pci_address"},
		{definition: PID, id: "xid.event.pid", unit: "1"},
		{definition: ProcessName, id: "xid.event.process_name"},
		{definition: GPC, id: "xid.event.gpc", unit: "1"},
		{definition: TPC, id: "xid.event.tpc", unit: "1"},
		{definition: SM, id: "xid.event.sm", unit: "1"},
		{definition: NVLink5SubComponent, id: "xid.event.nvlink5.sub_component"},
		{definition: NVLink5Fatal, id: "xid.event.nvlink5.fatal"},
		{definition: NVLink5Link, id: "xid.event.nvlink5.link", unit: "1"},
		{definition: NVLink5IntrInfo, id: "xid.event.nvlink5.intr_info"},
		{definition: NVLink5ErrorStatus, id: "xid.event.nvlink5.error_status"},
		{definition: NVLink5DebugData, id: "xid.event.nvlink5.debug_data"},
		{definition: RecoveryActionFromCode, id: "xid.event.recovery_action.from_code"},
		{definition: RecoveryActionFromName, id: "xid.event.recovery_action.from_name"},
		{definition: RecoveryActionToCode, id: "xid.event.recovery_action.to_code"},
		{definition: RecoveryActionToName, id: "xid.event.recovery_action.to_name"},
	}
	all := All()
	require.Len(t, all, len(tests))
	for i, test := range tests {
		require.Equal(t, test.id, all[i].ID())
		require.Equal(t, test.id, test.definition.ID())
		if test.unit == "" {
			require.Nil(t, test.definition.Unit(), test.id)
		} else {
			require.Equal(t, test.unit, *test.definition.Unit(), test.id)
		}
	}
	require.Equal(t, all[1:], EventSignals())
}

func TestMatch(t *testing.T) {
	require.True(t, Match("NVRM: Xid (PCI:0000:01:00): 79"))
	require.False(t, Match("nvidia-nvswitch0: SXid (PCI:0000:06:00.0): 12028"))
	require.False(t, Match("NVRM: GPU 0000:18:00.0: GPU has fallen off the bus."))
	require.False(t, Match("Xid"))
	require.True(t, Match("NVRM: Xid (0000:03:00): 14, Channel 00000001"))
	require.True(t, Match("NVRM: Xid (PCI:0000:01:00 GPU-I:01): 79"))
	// Text a process can put in a kernel line through its own name lacks the
	// device group, so it never reaches Parse as an Xid line.
	require.False(t, Match("Out of memory: Killed process 4242 (NVRM: Xid x) total-vm:1024kB, anon-rss:512kB, file-rss:0kB"))
	require.False(t, Match("NVRM: Xid error: xyz, invalid data"))
	require.False(t, Match("NVRM: Xid (invalid): some error"))
}
