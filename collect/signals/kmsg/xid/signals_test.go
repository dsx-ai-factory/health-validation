// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPatterns(t *testing.T) {
	examples := map[string]string{
		"kernel.nvrm_xid_combined":               "NVRM: Xid (PCI:0000:05:00): 79, GPU has fallen off the bus.",
		"kernel.nvrm_xid_extended":               "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
		"kernel.nvrm_fallen_off_bus_multiline":   "NVRM: The NVIDIA GPU 0000:18:00.0\nNVRM: has fallen off the bus and is not responding to commands.",
		"kernel.nvrm_fallen_off_bus_single_line": "NVRM: GPU 0000:18:00.0: GPU has fallen off the bus.",
	}
	patterns := DefaultPatterns()
	require.Len(t, patterns, len(examples))
	for id, expression := range patterns {
		t.Run(id, func(t *testing.T) {
			re, err := regexp.Compile(expression)
			require.NoError(t, err)
			require.Contains(t, examples, id)
			require.True(t, re.MatchString(examples[id]), expression)
			require.False(t, re.MatchString("normal boot"), expression)
		})
	}
	original := patterns["kernel.nvrm_xid_combined"]
	patterns["kernel.nvrm_xid_combined"] = "mutated"
	require.Equal(t, original, DefaultPatterns()["kernel.nvrm_xid_combined"])
}
