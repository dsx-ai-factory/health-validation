// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package fuse defines raw FUSE queue and mount-correlation signals.
package fuse

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Waiting             = collect.NewSignal("fuse.waiting", "1")
	MaxBackground       = collect.NewSignal("fuse.max_background", "1")
	CongestionThreshold = collect.NewSignal("fuse.congestion_threshold", "1")
	// Mounts contains newline-separated strconv.Quote(namespace+"/"+mountID)
	// records. Each record names a MountRef; bind mounts remain distinct.
	// An empty string means this connection has no mounts in the selected view.
	// The same empty-list representation applies to FSTypes and Sources.
	Mounts = collect.NewSignal("fuse.mounts", "")
	// FSTypes uses the same quoted identity, followed by "=" and a quoted fstype.
	FSTypes = collect.NewSignal("fuse.mount.fstypes", "")
	// Sources uses the same quoted identity, followed by "=" and a quoted source.
	// Quoting keeps embedded spaces, newlines, and equals signs unambiguous.
	Sources = collect.NewSignal("fuse.mount.sources", "")
)

func All() collect.Signals {
	return collect.Signals{Waiting, MaxBackground, CongestionThreshold, Mounts, FSTypes, Sources}
}

func IsMount(signal collect.Signal) bool {
	return signal == Mounts || signal == FSTypes || signal == Sources
}
