// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPatterns(t *testing.T) {
	examples := map[string]string{
		"kernel.panic":                           "Kernel panic - not syncing",
		"kernel.oom":                             "postgres invoked oom-killer: gfp_mask=0x201d2",
		"kernel.oom_legacy_container":            "Task in /a killed as a result of limit of /b",
		"kernel.oom_container":                   "oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/a,task_memcg=/a,task=python,pid=42,uid=0",
		"kernel.oom_killed_process":              "Out of memory: Killed process 42 (python)",
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
	original := patterns["kernel.panic"]
	patterns["kernel.panic"] = "mutated"
	require.Equal(t, original, DefaultPatterns()["kernel.panic"])
}
