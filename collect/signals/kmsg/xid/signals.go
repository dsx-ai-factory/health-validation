// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package xid defines opt-in NVIDIA Xid kernel-log patterns.
package xid

// DefaultPatterns returns an independent signal-ID-to-regex map for callers to
// opt into. Regexes are copied unchanged from GPUd at
// ea5ab612abad200c7793220f0e7955bb7c8f6302. Matching is per record;
// multi-record reconstruction and health policy remain with the consumer.
func DefaultPatterns() map[string]string {
	return map[string]string{
		"kernel.nvrm_xid_combined":               `NVRM: Xid \(((?:PCI:)?[0-9a-fA-F:]+)\).*?: (\d+),`,
		"kernel.nvrm_xid_extended":               `NVRM: Xid \(PCI:([0-9a-fA-F:]+)\): (\d+)(?:, pid=(\d+), name=([^,]+))?, ([A-Z_]+(?:/[A-Z_]+)?)\s+(Nonfatal|Fatal)\s+(XC[01])\s+(i\d+)\s+Link\s+(-?\d+)\s+\((0x[0-9a-fA-F]+)\s+(0x[0-9a-fA-F]+)(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?`,
		"kernel.nvrm_fallen_off_bus_multiline":   `(?s)NVRM:\s+The NVIDIA GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0.*?fallen off the bus and is not responding to commands\.`,
		"kernel.nvrm_fallen_off_bus_single_line": `NVRM:\s+GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0:\s+GPU has fallen off the bus\.?`,
	}
}
