// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package memory defines opt-in OOM and kernel-panic kernel-log patterns.
package memory

// Stable signal IDs for consumers of kernel-log observations.
const (
	Panic              = "kernel.panic"
	OOM                = "kernel.oom"
	OOMLegacyContainer = "kernel.oom_legacy_container"
	OOMContainer       = "kernel.oom_container"
	OOMKilledProcess   = "kernel.oom_killed_process"
)

// DefaultPatterns returns an independent signal-ID-to-regex map for callers to
// opt into. Regexes are copied unchanged from GPUd at
// ea5ab612abad200c7793220f0e7955bb7c8f6302. Matching is per record;
// multi-record reconstruction and health policy remain with the consumer.
func DefaultPatterns() map[string]string {
	return map[string]string{
		Panic: `Kernel [Pp]anic`,

		OOM:                `invoked oom-killer:`,
		OOMLegacyContainer: `Task in (.*) killed as a result of limit of (.*)`,
		OOMContainer:       `oom-kill:constraint=(.*),nodemask=(.*),cpuset=(.*),mems_allowed=(.*),oom_memcg=(.*),task_memcg=(.*),task=(.*),pid=(.*),uid=(.*)`,
		OOMKilledProcess:   `Killed process ([0-9]+) \((.+)\)`,
	}
}
