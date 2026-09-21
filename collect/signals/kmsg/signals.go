// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmsg defines kernel-record signals and opt-in GPUd regexes.
package kmsg

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Message  = collect.NewSignal("kernel.message", "")
	Priority = collect.NewSignal("kernel.priority", "1")

	Panic = collect.NewSignal("kernel.panic", "")

	OOM                = collect.NewSignal("kernel.oom", "")
	OOMLegacyContainer = collect.NewSignal("kernel.oom_legacy_container", "")
	OOMContainer       = collect.NewSignal("kernel.oom_container", "")
	OOMKilledProcess   = collect.NewSignal("kernel.oom_killed_process", "")

	NVRMXidCombined            = collect.NewSignal("kernel.nvrm_xid_combined", "")
	NVRMXidExtended            = collect.NewSignal("kernel.nvrm_xid_extended", "")
	NVRMFallenOffBusMultiline  = collect.NewSignal("kernel.nvrm_fallen_off_bus_multiline", "")
	NVRMFallenOffBusSingleLine = collect.NewSignal("kernel.nvrm_fallen_off_bus_single_line", "")
)

// DefaultPatterns returns an independent signal-ID-to-regex map for callers to
// opt into. Regexes are copied unchanged from GPUd at
// ea5ab612abad200c7793220f0e7955bb7c8f6302. Matching is per record;
// multi-record reconstruction and health policy remain with the consumer.
func DefaultPatterns() map[string]string {
	return map[string]string{
		// components/os/kmsg_matcher.go
		Panic.ID(): `Kernel [Pp]anic`,

		// components/memory/kmsg_matcher.go
		OOM.ID():                `invoked oom-killer:`,
		OOMLegacyContainer.ID(): `Task in (.*) killed as a result of limit of (.*)`,
		OOMContainer.ID():       `oom-kill:constraint=(.*),nodemask=(.*),cpuset=(.*),mems_allowed=(.*),oom_memcg=(.*),task_memcg=(.*),task=(.*),pid=(.*),uid=(.*)`,
		OOMKilledProcess.ID():   `Killed process ([0-9]+) \((.+)\)`,

		// components/accelerator/nvidia/xid/kmsg.go
		NVRMXidCombined.ID():            `NVRM: Xid \(((?:PCI:)?[0-9a-fA-F:]+)\).*?: (\d+),`,
		NVRMXidExtended.ID():            `NVRM: Xid \(PCI:([0-9a-fA-F:]+)\): (\d+)(?:, pid=(\d+), name=([^,]+))?, ([A-Z_]+(?:/[A-Z_]+)?)\s+(Nonfatal|Fatal)\s+(XC[01])\s+(i\d+)\s+Link\s+(-?\d+)\s+\((0x[0-9a-fA-F]+)\s+(0x[0-9a-fA-F]+)(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?(?:\s+(0x[0-9a-fA-F]+))?`,
		NVRMFallenOffBusMultiline.ID():  `(?s)NVRM:\s+The NVIDIA GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0.*?fallen off the bus and is not responding to commands\.`,
		NVRMFallenOffBusSingleLine.ID(): `NVRM:\s+GPU ((?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-9a-fA-F]{2})\.0:\s+GPU has fallen off the bus\.?`,
	}
}

func All() collect.Signals {
	return collect.Signals{
		Message, Priority,
		Panic, OOM, OOMLegacyContainer, OOMContainer, OOMKilledProcess,
		NVRMXidCombined, NVRMXidExtended,
		NVRMFallenOffBusMultiline, NVRMFallenOffBusSingleLine,
	}
}
