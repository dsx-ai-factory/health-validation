// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package process defines current process snapshots, not duration tracking.
package process

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/collect"
)

var (
	Name             = collect.NewSignal("process.name", "")
	State            = collect.NewSignal("process.state", "")
	StartTimeTicks   = collect.NewSignal("process.start_time_ticks", "clock_ticks")
	Count            = collect.NewSignal("process.count", "1")
	RunningCount     = collect.NewSignal("process.state.running.count", "1")
	SleepingCount    = collect.NewSignal("process.state.sleeping.count", "1")
	BlockedCount     = collect.NewSignal("process.state.blocked.count", "1")
	ZombieCount      = collect.NewSignal("process.state.zombie.count", "1")
	StoppedCount     = collect.NewSignal("process.state.stopped.count", "1")
	TracingStopCount = collect.NewSignal("process.state.tracing_stop.count", "1")
	DeadCount        = collect.NewSignal("process.state.dead.count", "1")
	IdleCount        = collect.NewSignal("process.state.idle.count", "1")
	ParkedCount      = collect.NewSignal("process.state.parked.count", "1")
	WakingCount      = collect.NewSignal("process.state.waking.count", "1")
	WakekillCount    = collect.NewSignal("process.state.wakekill.count", "1")
)

// StateCount maps each procfs state letter to its canonical count signal.
func StateCount(state string) collect.Signal {
	switch state {
	case "R":
		return RunningCount
	case "S":
		return SleepingCount
	case "D":
		return BlockedCount
	case "Z":
		return ZombieCount
	case "T":
		return StoppedCount
	case "t":
		return TracingStopCount
	case "X", "x":
		return DeadCount
	case "I":
		return IdleCount
	case "P":
		return ParkedCount
	case "W":
		return WakingCount
	case "K":
		return WakekillCount
	default:
		return nil
	}
}

func Counts() collect.Signals {
	return collect.Signals{Count, RunningCount, SleepingCount, BlockedCount, ZombieCount, StoppedCount, TracingStopCount, DeadCount, IdleCount, ParkedCount, WakingCount, WakekillCount}
}

func All() collect.Signals {
	return slices.Concat(collect.Signals{Name, State, StartTimeTicks}, Counts())
}
