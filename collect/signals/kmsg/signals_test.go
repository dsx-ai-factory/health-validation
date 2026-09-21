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
		Panic.ID():                      "Kernel panic - not syncing",
		OOM.ID():                        "postgres invoked oom-killer: gfp_mask=0x201d2",
		OOMLegacyContainer.ID():         "Task in /a killed as a result of limit of /b",
		OOMContainer.ID():               "oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/a,task_memcg=/a,task=python,pid=42,uid=0",
		OOMKilledProcess.ID():           "Out of memory: Killed process 42 (python)",
		NVRMXidCombined.ID():            "NVRM: Xid (PCI:0000:05:00): 79, GPU has fallen off the bus.",
		NVRMXidExtended.ID():            "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
		NVRMFallenOffBusMultiline.ID():  "NVRM: The NVIDIA GPU 0000:18:00.0\nNVRM: has fallen off the bus and is not responding to commands.",
		NVRMFallenOffBusSingleLine.ID(): "NVRM: GPU 0000:18:00.0: GPU has fallen off the bus.",
	}
	patterns := DefaultPatterns()
	require.Len(t, patterns, len(examples))
	require.Len(t, All(), len(patterns)+2)
	for _, signal := range All()[2:] {
		t.Run(signal.ID(), func(t *testing.T) {
			expression, ok := patterns[signal.ID()]
			require.True(t, ok)
			re, err := regexp.Compile(expression)
			require.NoError(t, err)
			require.Contains(t, examples, signal.ID())
			require.True(t, re.MatchString(examples[signal.ID()]), expression)
			require.False(t, re.MatchString("normal boot"), expression)
		})
	}
	original := patterns[Panic.ID()]
	patterns[Panic.ID()] = "mutated"
	require.Equal(t, original, DefaultPatterns()[Panic.ID()])
}
