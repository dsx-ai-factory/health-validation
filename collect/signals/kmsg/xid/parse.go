// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Helpers copied from GPUd components/accelerator/nvidia/xid/kmsg.go at
// ea5ab612abad200c7793220f0e7955bb7c8f6302.
package xid

import (
	"regexp"
	"strconv"
)

var compiledRegexNVRMXidCombined = regexp.MustCompile(DefaultPatterns()["kernel.nvrm_xid_combined"])

// ExtractNVRMXidInfo extracts the explicit Xid number and PCI device identifier
// from raw kernel message text. It returns (0, "") when no number is found.
// Fallen-off-bus inference and multi-record reconstruction are not performed.
func ExtractNVRMXidInfo(line string) (int, string) {
	if match := compiledRegexNVRMXidCombined.FindStringSubmatch(line); match != nil {
		if id, err := strconv.Atoi(match[2]); err == nil {
			return id, match[1]
		}
	}
	return 0, ""
}

// ExtractNVRMXid extracts the nvidia Xid error code from the dmesg log line.
// Returns 0 if the error code is not found.
// https://docs.nvidia.com/deploy/pdf/XID_Errors.pdf
func ExtractNVRMXid(line string) int {
	id, _ := ExtractNVRMXidInfo(line)
	return id
}
