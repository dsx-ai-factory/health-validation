// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmsg defines kernel-record signals and GPUd-derived match patterns.
package kmsg

import "github.com/dsx-ai-factory/health-validation/collect"

// Pattern assigns a stable signal ID to a regex. Matches carry the exact regex.
type Pattern struct {
	SignalID string
	Regex    string
}

var (
	Message                    = collect.NewSignal("kernel.message", "")
	Priority                   = collect.NewSignal("kernel.priority", "1")
	Panic                      = collect.NewSignal("kernel.panic", "")
	FileMax                    = collect.NewSignal("kernel.file_max", "")
	HungTask                   = collect.NewSignal("kernel.hung_task", "")
	SoftLockup                 = collect.NewSignal("kernel.soft_lockup", "")
	OOM                        = collect.NewSignal("kernel.oom", "")
	OOMLegacyContainer         = collect.NewSignal("kernel.oom_legacy_container", "")
	OOMContainer               = collect.NewSignal("kernel.oom_container", "")
	OOMKilledProcess           = collect.NewSignal("kernel.oom_killed_process", "")
	RAIDArrayFailure           = collect.NewSignal("kernel.raid_array_failure", "")
	FilesystemReadOnly         = collect.NewSignal("kernel.filesystem_read_only", "")
	NVMePathFailure            = collect.NewSignal("kernel.nvme_path_failure", "")
	NVMeTimeout                = collect.NewSignal("kernel.nvme_timeout", "")
	NVMeDeviceDisabled         = collect.NewSignal("kernel.nvme_device_disabled", "")
	BeyondEndOfDevice          = collect.NewSignal("kernel.beyond_end_of_device", "")
	BufferIOError              = collect.NewSignal("kernel.buffer_io_error", "")
	SuperblockWriteError       = collect.NewSignal("kernel.superblock_write_error", "")
	NFSServerNotResponding     = collect.NewSignal("kernel.nfs_server_not_responding", "")
	NFSServerOK                = collect.NewSignal("kernel.nfs_server_ok", "")
	NFSLockReclaimFailed       = collect.NewSignal("kernel.nfs_lock_reclaim_failed", "")
	NFSWritebackHang           = collect.NewSignal("kernel.nfs_writeback_hang", "")
	PCIPowerInsufficient       = collect.NewSignal("kernel.pci_power_insufficient", "")
	PortModuleHighTemperature  = collect.NewSignal("kernel.port_module_high_temperature", "")
	AccessRegFailed            = collect.NewSignal("kernel.access_reg_failed", "")
	NVRMXidCombined            = collect.NewSignal("kernel.nvrm_xid_combined", "")
	NVRMXidExtended            = collect.NewSignal("kernel.nvrm_xid_extended", "")
	NVRMFallenOffBusMultiline  = collect.NewSignal("kernel.nvrm_fallen_off_bus_multiline", "")
	NVRMFallenOffBusSingleLine = collect.NewSignal("kernel.nvrm_fallen_off_bus_single_line", "")
	NVSwitchSXid               = collect.NewSignal("kernel.nvswitch_sxid", "")
	NVLinkPostRxDetectFailure  = collect.NewSignal("kernel.nvlink_post_rx_detect_failure", "")
	PeermemInvalidContext      = collect.NewSignal("kernel.peermem_invalid_context", "")
	NCCLSegfaultInLibnccl      = collect.NewSignal("kernel.nccl_segfault_in_libnccl", "")
)

// Regexes are copied from GPUd at ea5ab612abad200c7793220f0e7955bb7c8f6302.
// They select individual records; multi-record reconstruction and health policy
// remain with the consumer. A multiline regex only matches within one record.
const (
	// components/os/kmsg_matcher.go
	RegexPanic   = `Kernel [Pp]anic`
	RegexFileMax = `VFS: file-max limit \d+ reached`
	// components/cpu/kmsg_matcher.go
	RegexHungTask   = `(?:INFO: )?task ([^:]+:[\d]+).+blocked for more than \d+ seconds`
	RegexSoftLockup = `soft lockup - CPU#\d+ stuck for \d+s! \[([^:]+:[\d]+)\]`
	// components/memory/kmsg_matcher.go
	RegexOOM                = `invoked oom-killer:`
	RegexOOMLegacyContainer = `Task in (.*) killed as a result of limit of (.*)`
	RegexOOMContainer       = `oom-kill:constraint=(.*),nodemask=(.*),cpuset=(.*),mems_allowed=(.*),oom_memcg=(.*),task_memcg=(.*),task=(.*),pid=(.*),uid=(.*)`
	RegexOOMKilledProcess   = `Killed process ([0-9]+) \((.+)\)`
	// components/disk/kmsg_matcher.go
	RegexRAIDArrayFailure     = `md/raid.*: Disk failure on .* detected, failing array`
	RegexFilesystemReadOnly   = `.*Remounting filesystem read-only`
	RegexNVMePathFailure      = `block nvme.*: no available path - failing I/O`
	RegexNVMeTimeout          = `nvme nvme[0-9]+: I/O .* timeout, reset controller`
	RegexNVMeDeviceDisabled   = `nvme nvme[0-9]+: Disabling device after reset failure`
	RegexBeyondEndOfDevice    = `attempt to access beyond end of device`
	RegexBufferIOError        = `Buffer I/O error on dev [^ ]+, logical block [0-9]+`
	RegexSuperblockWriteError = `I/O error while writing superblock`
	// components/nfs/kmsg_matcher.go
	RegexNFSServerNotResponding = `nfs: server ([^ ]+) not responding`
	RegexNFSServerOK            = `nfs: server ([^ ]+) OK`
	RegexNFSLockReclaimFailed   = `nfs4_reclaim_open_state: Lock reclaim failed`
	RegexNFSWritebackHang       = `(^|\s)(nfs_lock_and_join_requests|nfs_wb_all|nfs_page_async_flush|nfs_writepages_callback)\+0x[0-9a-f]+`
	// components/accelerator/nvidia/infiniband/kmsg_matcher.go
	RegexPCIPowerInsufficient      = `Detected insufficient power on the PCIe slot \(([0-9]+W)\)`
	RegexPortModuleHighTemperature = `Port module event.*High Temperature`
	RegexAccessRegFailed           = `mlx5_cmd_out_err.*ACCESS_REG.*failed`
	// components/accelerator/nvidia/xid/kmsg.go
	RegexNVRMXidCombined            = `NVRM: Xid \(((?:PCI:)?[0-9a-fA-F:]+)\).*?: (\d+),`
	RegexNVRMXidExtended            = `NVRM: Xid \(PCI:([0-9a-fA-F:]+)\): (\d+)(?:, pid=(\d+), name=([^,]+))?, ([A-Z_]+(?:/[A-Z_]+)?)\s+(Nonfatal|Fatal)\s+(XC[01])\s+(i\d+)\s+Link\s+(-?\d+)\s+\((0x[0-9a-fA-F]+)\s+(0x[0-9a-fA-F]+)(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?`
	RegexNVRMFallenOffBusMultiline  = `(?s)NVRM:\s+The NVIDIA GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0.*?fallen off the bus and is not responding to commands\.`
	RegexNVRMFallenOffBusSingleLine = `NVRM:\s+GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0:\s+GPU has fallen off the bus\.?`
	// components/accelerator/nvidia/sxid/kmsg.go
	RegexNVSwitchSXid = `SXid.*?: (\d+),`
	// components/accelerator/nvidia/nvlink/kmsg.go
	RegexNVLinkPostRxDetectFailure = `NVRM: (?:knvlinkUpdatePostRxDetectLinkMask_[A-Za-z0-9_]+: Failed to update Rx Detect Link mask!|knvlinkDiscoverPostRxDetLinks_[A-Za-z0-9_]+: Getting peer[0-9]+(?:'s)? postRxDetLinkMask failed!)`
	// components/accelerator/nvidia/peermem/kmsg_matcher.go
	RegexPeermemInvalidContext = `.*ERROR detected invalid context, skipping further processing`
	// components/accelerator/nvidia/nccl/kmsg_matcher.go
	RegexNCCLSegfaultInLibnccl = `.*segfault at.*in libnccl\.so.*`
)

// DefaultPatterns returns an independent set of GPUd kernel-log patterns.
func DefaultPatterns() []Pattern {
	return []Pattern{
		{SignalID: Panic.ID(), Regex: RegexPanic},
		{SignalID: FileMax.ID(), Regex: RegexFileMax},
		{SignalID: HungTask.ID(), Regex: RegexHungTask},
		{SignalID: SoftLockup.ID(), Regex: RegexSoftLockup},
		{SignalID: OOM.ID(), Regex: RegexOOM},
		{SignalID: OOMLegacyContainer.ID(), Regex: RegexOOMLegacyContainer},
		{SignalID: OOMContainer.ID(), Regex: RegexOOMContainer},
		{SignalID: OOMKilledProcess.ID(), Regex: RegexOOMKilledProcess},
		{SignalID: RAIDArrayFailure.ID(), Regex: RegexRAIDArrayFailure},
		{SignalID: FilesystemReadOnly.ID(), Regex: RegexFilesystemReadOnly},
		{SignalID: NVMePathFailure.ID(), Regex: RegexNVMePathFailure},
		{SignalID: NVMeTimeout.ID(), Regex: RegexNVMeTimeout},
		{SignalID: NVMeDeviceDisabled.ID(), Regex: RegexNVMeDeviceDisabled},
		{SignalID: BeyondEndOfDevice.ID(), Regex: RegexBeyondEndOfDevice},
		{SignalID: BufferIOError.ID(), Regex: RegexBufferIOError},
		{SignalID: SuperblockWriteError.ID(), Regex: RegexSuperblockWriteError},
		{SignalID: NFSServerNotResponding.ID(), Regex: RegexNFSServerNotResponding},
		{SignalID: NFSServerOK.ID(), Regex: RegexNFSServerOK},
		{SignalID: NFSLockReclaimFailed.ID(), Regex: RegexNFSLockReclaimFailed},
		{SignalID: NFSWritebackHang.ID(), Regex: RegexNFSWritebackHang},
		{SignalID: PCIPowerInsufficient.ID(), Regex: RegexPCIPowerInsufficient},
		{SignalID: PortModuleHighTemperature.ID(), Regex: RegexPortModuleHighTemperature},
		{SignalID: AccessRegFailed.ID(), Regex: RegexAccessRegFailed},
		{SignalID: NVRMXidCombined.ID(), Regex: RegexNVRMXidCombined},
		{SignalID: NVRMXidExtended.ID(), Regex: RegexNVRMXidExtended},
		{SignalID: NVRMFallenOffBusMultiline.ID(), Regex: RegexNVRMFallenOffBusMultiline},
		{SignalID: NVRMFallenOffBusSingleLine.ID(), Regex: RegexNVRMFallenOffBusSingleLine},
		{SignalID: NVSwitchSXid.ID(), Regex: RegexNVSwitchSXid},
		{SignalID: NVLinkPostRxDetectFailure.ID(), Regex: RegexNVLinkPostRxDetectFailure},
		{SignalID: PeermemInvalidContext.ID(), Regex: RegexPeermemInvalidContext},
		{SignalID: NCCLSegfaultInLibnccl.ID(), Regex: RegexNCCLSegfaultInLibnccl},
	}
}

func All() collect.Signals {
	return collect.Signals{Message, Priority, Panic, FileMax, HungTask, SoftLockup, OOM, OOMLegacyContainer, OOMContainer, OOMKilledProcess, RAIDArrayFailure, FilesystemReadOnly, NVMePathFailure, NVMeTimeout, NVMeDeviceDisabled, BeyondEndOfDevice, BufferIOError, SuperblockWriteError, NFSServerNotResponding, NFSServerOK, NFSLockReclaimFailed, NFSWritebackHang, PCIPowerInsufficient, PortModuleHighTemperature, AccessRegFailed, NVRMXidCombined, NVRMXidExtended, NVRMFallenOffBusMultiline, NVRMFallenOffBusSingleLine, NVSwitchSXid, NVLinkPostRxDetectFailure, PeermemInvalidContext, NCCLSegfaultInLibnccl}
}
