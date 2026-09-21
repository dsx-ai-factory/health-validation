// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package memory defines opt-in OOM and kernel-panic kernel-log patterns.
package memory

// DefaultPatterns returns an independent signal-ID-to-regex map for callers to
// opt into. Regexes are copied unchanged from GPUd at
// ea5ab612abad200c7793220f0e7955bb7c8f6302. Matching is per record;
// multi-record reconstruction and health policy remain with the consumer.
func DefaultPatterns() map[string]string {
	return map[string]string{
		"kernel.panic": `Kernel [Pp]anic`,

		"kernel.oom":                  `invoked oom-killer:`,
		"kernel.oom_legacy_container": `Task in (.*) killed as a result of limit of (.*)`,
		"kernel.oom_container":        `oom-kill:constraint=(.*),nodemask=(.*),cpuset=(.*),mems_allowed=(.*),oom_memcg=(.*),task_memcg=(.*),task=(.*),pid=(.*),uid=(.*)`,
		"kernel.oom_killed_process":   `Killed process ([0-9]+) \((.+)\)`,
	}
}
