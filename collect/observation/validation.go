// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"fmt"
	"math/bits"
	"strings"
)

// validateDCGMHealthValue checks a health report's structure and availability masks.
// Native codes remain unrestricted so newer DCGM results survive validation.
func validateDCGMHealthValue(value *Value) error {
	kind, ok := value.GetKind().(*Value_DCGMHealthReport)
	if !ok {
		return nil
	}
	if kind == nil || kind.DcgmHealthReport == nil {
		return fmt.Errorf("value has an empty DCGM health report payload")
	}
	report := kind.DcgmHealthReport
	requestedSystems := report.GetRequestedSystems()
	systemsWithoutTargets := report.GetSystemsWithoutTargets()

	// The missing-target mask describes only checks the caller requested.
	if systemsWithoutTargets&requestedSystems != systemsWithoutTargets {
		return fmt.Errorf("DCGM health report systems_without_targets includes unrequested systems")
	}
	for system, failure := range report.GetSystemErrors() {
		if bits.OnesCount64(system) != 1 {
			return fmt.Errorf("DCGM health report system_errors key must identify one subsystem")
		}
		if system&requestedSystems == 0 {
			return fmt.Errorf("DCGM health report system_errors includes an unrequested system")
		}
		if system&systemsWithoutTargets != 0 {
			return fmt.Errorf("DCGM health report failed system cannot also have no targets")
		}
		if failure == nil {
			return fmt.Errorf("DCGM health report system_errors requires an error for each system")
		}
	}
	for index, group := range report.GetGroupResults() {
		if group == nil {
			return fmt.Errorf("DCGM health report group result %d is required", index)
		}
		if group.GetSystems() == 0 || group.GetSystems()&requestedSystems != group.GetSystems() {
			return fmt.Errorf("DCGM health report group result %d must identify requested systems", index)
		}
		if group.GetCollectionError() != nil && group.GetSystems()&systemsWithoutTargets != 0 {
			return fmt.Errorf("DCGM health report failed group %d cannot also have no targets", index)
		}
		if since := group.GetMonitoredSince(); since != nil {
			if err := since.CheckValid(); err != nil {
				return fmt.Errorf("DCGM health report group result %d monitored since: %w", index, err)
			}
		}
		for entityIndex, result := range group.GetEntityResults() {
			if result == nil || result.GetEntity() == nil {
				return fmt.Errorf("DCGM health report group %d entity result %d requires an entity", index, entityIndex)
			}
			if err := validateDCGMHealthTarget(result.GetEntity()); err != nil {
				return fmt.Errorf("DCGM health report group %d entity result %d: %w", index, entityIndex, err)
			}
			if result.GroupMember == nil && group.GetCollectionError() == nil {
				return fmt.Errorf("DCGM health report group %d entity result %d requires group membership", index, entityIndex)
			}
			for _, failure := range result.GetCollectionErrors() {
				if failure == nil || failure.GetError() == nil {
					return fmt.Errorf("DCGM health report group %d entity result %d requires a collection error", index, entityIndex)
				}
				if failure.GetSystems() == 0 || failure.GetSystems()&group.GetSystems() != failure.GetSystems() {
					return fmt.Errorf("DCGM health report group %d entity result %d error must identify requested systems in its group", index, entityIndex)
				}
				if failure.GetSystems()&systemsWithoutTargets != 0 {
					return fmt.Errorf("DCGM health report group %d entity result %d error cannot also have no targets", index, entityIndex)
				}
			}
			for _, incident := range result.GetIncidents() {
				if incident == nil {
					return fmt.Errorf("DCGM health report group %d entity result %d has an empty incident", index, entityIndex)
				}
			}
		}
	}
	return nil
}

// validateDCGMHealthTarget checks a report's entity reference.
func validateDCGMHealthTarget(target *ObservationTarget) error {
	switch target.GetKind().(type) {
	case *ObservationTarget_GPU, *ObservationTarget_NVSwitch, *ObservationTarget_NativeEntity:
		return validateTarget(target)
	default:
		return fmt.Errorf("health entity must identify a GPU, NVSwitch, or native entity")
	}
}

// validateNativeEntityRef requires a source namespace when native metadata is present.
func validateNativeEntityRef(native *NativeEntityRef) error {
	if native != nil && strings.TrimSpace(native.Source) == "" {
		return fmt.Errorf("native entity requires a source")
	}
	return nil
}

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

// validateGPUProcessRef checks the GPU reference and process lifetime identity.
func validateGPUProcessRef(process *GPUProcessRef) error {
	if process == nil || process.Gpu == nil || process.Process == nil {
		return fmt.Errorf("target has an incomplete GPU process payload")
	}
	if err := validateNativeEntityRef(process.Gpu.GetNative()); err != nil {
		return err
	}
	if strings.TrimSpace(process.Gpu.Uuid) == "" && strings.TrimSpace(process.Gpu.SourceLocalId) == "" && process.Gpu.GetNative() == nil {
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
	if len(address) < 12 || len(address) > 16 {
		return fmt.Errorf("PCI target requires a canonical domain-qualified address")
	}
	domainEnd := len(address) - 8
	if address[domainEnd] != ':' || address[domainEnd+3] != ':' || address[domainEnd+6] != '.' {
		return fmt.Errorf("PCI target requires a canonical domain-qualified address")
	}
	for index := range address {
		if index == domainEnd || index == domainEnd+3 || index == domainEnd+6 || index == domainEnd+7 {
			continue
		}
		if !lowerHex(address[index]) {
			return fmt.Errorf("PCI target requires a lowercase hexadecimal address")
		}
	}
	if address[domainEnd+4] > '1' || address[domainEnd+7] < '0' || address[domainEnd+7] > '7' {
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
