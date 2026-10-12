// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package disk defines block-device inventory and filesystem signals.
package disk

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Name       = collect.NewSignal("disk.name", "")
	Type       = collect.NewSignal("disk.type", "")
	Major      = collect.NewSignal("disk.major", "1")
	Minor      = collect.NewSignal("disk.minor", "1")
	Serial     = collect.NewSignal("disk.serial", "")
	WWN        = collect.NewSignal("disk.wwn", "")
	Model      = collect.NewSignal("disk.model", "")
	Vendor     = collect.NewSignal("disk.vendor", "")
	Revision   = collect.NewSignal("disk.revision", "")
	Rotational = collect.NewSignal("disk.rotational", "1")
	SizeBytes  = collect.NewSignal("disk.size_bytes", "bytes")
	// Parents is a sorted comma-separated list of backing major:minor identities.
	Parents             = collect.NewSignal("disk.parents", "")
	MountPoint          = collect.NewSignal("disk.mount.point", "")
	MountRoot           = collect.NewSignal("disk.mount.root", "")
	MountParentID       = collect.NewSignal("disk.mount.parent_id", "1")
	MountOptions        = collect.NewSignal("disk.mount.options", "")
	MountOptionalFields = collect.NewSignal("disk.mount.optional_fields", "")
	MountSuperOptions   = collect.NewSignal("disk.mount.super_options", "")
	MountFSType         = collect.NewSignal("disk.mount.fstype", "")
	MountSource         = collect.NewSignal("disk.mount.source", "")
	MountDevice         = collect.NewSignal("disk.mount.device", "")
	TotalBytes          = collect.NewSignal("disk.filesystem.total_bytes", "bytes")
	FreeBytes           = collect.NewSignal("disk.filesystem.free_bytes", "bytes")
	UsedBytes           = collect.NewSignal("disk.filesystem.used_bytes", "bytes")
)

func All() collect.Signals {
	return collect.Signals{
		Name, Type, Major, Minor, Serial, WWN, Model, Vendor, Revision, Rotational, SizeBytes, Parents,
		MountPoint, MountRoot, MountParentID, MountOptions, MountOptionalFields, MountSuperOptions, MountFSType, MountSource, MountDevice,
		TotalBytes, FreeBytes, UsedBytes,
	}
}

func IsMount(signal collect.Signal) bool {
	return len(signal.ID()) >= len("disk.mount.") && signal.ID()[:len("disk.mount.")] == "disk.mount." || IsCapacity(signal)
}

func IsCapacity(signal collect.Signal) bool {
	return signal == TotalBytes || signal == FreeBytes || signal == UsedBytes
}
