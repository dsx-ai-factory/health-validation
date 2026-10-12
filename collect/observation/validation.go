// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"fmt"
	"strings"
)

func validateProcessRef(process *ProcessRef) error {
	if process == nil {
		return fmt.Errorf("target has an empty process payload")
	}
	if strings.TrimSpace(process.BootId) == "" {
		return fmt.Errorf("process target requires a boot ID")
	}
	if process.Pid == 0 {
		return fmt.Errorf("process target requires a nonzero PID")
	}
	// Zero start ticks are valid for processes created in the first kernel tick.
	return nil
}

func validateGPUProcessRef(process *GPUProcessRef) error {
	if process == nil || process.Gpu == nil || process.Process == nil {
		return fmt.Errorf("target has an incomplete GPU process payload")
	}
	if strings.TrimSpace(process.Gpu.Uuid) == "" && strings.TrimSpace(process.Gpu.SourceLocalId) == "" {
		return fmt.Errorf("GPU process target requires a GPU identity")
	}
	return validateProcessRef(process.Process)
}

func validateMountRef(mount *MountRef) error {
	if mount == nil {
		return fmt.Errorf("target has an empty mount payload")
	}
	if strings.TrimSpace(mount.Namespace) == "" {
		return fmt.Errorf("mount target requires a namespace")
	}
	if mount.MountId == 0 {
		return fmt.Errorf("mount target requires a nonzero mount ID")
	}
	return nil
}

func validatePCIRef(pci *PCIRef) error {
	if pci == nil {
		return fmt.Errorf("target has an empty PCI payload")
	}
	address := pci.Address
	if len(address) != 12 || address[4] != ':' || address[7] != ':' || address[10] != '.' {
		return fmt.Errorf("PCI target requires a canonical domain-qualified address")
	}
	for _, index := range [...]int{0, 1, 2, 3, 5, 6, 8, 9} {
		if !lowerHex(address[index]) {
			return fmt.Errorf("PCI target requires a lowercase hexadecimal address")
		}
	}
	if address[8] > '1' || address[11] < '0' || address[11] > '7' {
		return fmt.Errorf("PCI target has an invalid device or function number")
	}
	return nil
}

func validateFUSEConnectionRef(connection *FUSEConnectionRef) error {
	if connection == nil {
		return fmt.Errorf("target has an empty FUSE connection payload")
	}
	if strings.TrimSpace(connection.BootId) == "" {
		return fmt.Errorf("FUSE connection target requires a boot ID")
	}
	if connection.ConnectionId == 0 {
		return fmt.Errorf("FUSE connection target requires a nonzero connection ID")
	}
	return nil
}

func validatePstoreRecordRef(record *PstoreRecordRef) error {
	if record == nil {
		return fmt.Errorf("target has an empty pstore record payload")
	}
	if strings.TrimSpace(record.Path) == "" {
		return fmt.Errorf("pstore record target requires a path")
	}
	if len(record.Sha256) != 64 {
		return fmt.Errorf("pstore record target requires a SHA256 digest")
	}
	for index := range record.Sha256 {
		if !lowerHex(record.Sha256[index]) {
			return fmt.Errorf("pstore record digest must be lowercase hexadecimal")
		}
	}
	return nil
}

func lowerHex(value byte) bool {
	return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f')
}
