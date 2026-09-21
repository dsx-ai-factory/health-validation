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
		FileMax.ID():                    "VFS: file-max limit 1000000 reached",
		HungTask.ID():                   "INFO: task jfsmount:136986 blocked for more than 120 seconds.",
		SoftLockup.ID():                 "soft lockup - CPU#18 stuck for 27s! [python3:2254956]",
		OOM.ID():                        "postgres invoked oom-killer: gfp_mask=0x201d2",
		OOMLegacyContainer.ID():         "Task in /a killed as a result of limit of /b",
		OOMContainer.ID():               "oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/a,task_memcg=/a,task=python,pid=42,uid=0",
		OOMKilledProcess.ID():           "Out of memory: Killed process 42 (python)",
		RAIDArrayFailure.ID():           "md/raid0md0: Disk failure on nvme0n1p1 detected, failing array.",
		FilesystemReadOnly.ID():         "EXT4-fs (md0): Remounting filesystem read-only",
		NVMePathFailure.ID():            "block nvme0n1: no available path - failing I/O",
		NVMeTimeout.ID():                "nvme nvme4: I/O tag 22 (2016) opcode 0x2 (Admin Cmd) QID 0 timeout, reset controller",
		NVMeDeviceDisabled.ID():         "nvme nvme4: Disabling device after reset failure: -19",
		BeyondEndOfDevice.ID():          "kworker/u203:1: attempt to access beyond end of device",
		BufferIOError.ID():              "Buffer I/O error on dev dm-0, logical block 1308098575, lost async page write",
		SuperblockWriteError.ID():       "EXT4-fs (dm-0): I/O error while writing superblock",
		NFSServerNotResponding.ID():     "nfs: server example not responding, timed out",
		NFSServerOK.ID():                "nfs: server example OK",
		NFSLockReclaimFailed.ID():       "nfs4_reclaim_open_state: Lock reclaim failed!",
		NFSWritebackHang.ID():           " nfs_wb_all+0x2c/0x190 [nfs]",
		PCIPowerInsufficient.ID():       "Detected insufficient power on the PCIe slot (27W).",
		PortModuleHighTemperature.ID():  "Port module event[error]: module 0, Cable error, High Temperature",
		AccessRegFailed.ID():            "mlx5_cmd_out_err:838: ACCESS_REG(0x805) failed",
		NVRMXidCombined.ID():            "NVRM: Xid (PCI:0000:05:00): 79, GPU has fallen off the bus.",
		NVRMXidExtended.ID():            "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
		NVRMFallenOffBusMultiline.ID():  "NVRM: The NVIDIA GPU 0000:18:00.0\nNVRM: has fallen off the bus and is not responding to commands.",
		NVRMFallenOffBusSingleLine.ID(): "NVRM: GPU 0000:18:00.0: GPU has fallen off the bus.",
		NVSwitchSXid.ID():               "nvidia-nvswitch3: SXid (PCI:0000:05:00.0): 12028, Non-fatal",
		NVLinkPostRxDetectFailure.ID():  "NVRM: knvlinkUpdatePostRxDetectLinkMask_GH100: Failed to update Rx Detect Link mask!",
		PeermemInvalidContext.ID():      "nvidia-peermem: ERROR detected invalid context, skipping further processing",
		NCCLSegfaultInLibnccl.ID():      "process: segfault at 123 in libnccl.so.2",
	}
	patterns := DefaultPatterns()
	require.Len(t, patterns, len(examples))
	require.Len(t, All(), len(patterns)+2)
	seen := map[string]bool{Message.ID(): true, Priority.ID(): true}
	for i, pattern := range patterns {
		t.Run(pattern.SignalID, func(t *testing.T) {
			require.False(t, seen[pattern.SignalID], "duplicate signal")
			seen[pattern.SignalID] = true
			require.Equal(t, All()[i+2].ID(), pattern.SignalID)
			re, err := regexp.Compile(pattern.Regex)
			require.NoError(t, err)
			require.Contains(t, examples, pattern.SignalID)
			require.True(t, re.MatchString(examples[pattern.SignalID]), pattern.Regex)
			require.False(t, re.MatchString("normal boot"), pattern.Regex)
		})
	}
	patterns[0].Regex = "mutated"
	require.Equal(t, RegexPanic, DefaultPatterns()[0].Regex)
}
