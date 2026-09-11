//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type fieldTransform func(*observation.Value) (observation.Value, *observation.CollectionError)

type fieldMapping struct {
	transform fieldTransform
	signalID  string
	fieldID   godcgm.Short
}

// The clock-event masks are defined by DCGM in dcgm_fields.h.
// DCGM_FI_DEV_FABRIC_HEALTH_MASK uses the packed health-mask layout defined by
// NVML; DCGM's public access macros delegate to the NVML macros. The pinned
// go-dcgm bindings do not expose these constants in Go.
// See:
// https://docs.nvidia.com/datacenter/dcgm/latest/reference/dcgm-api/dcgm-api-field-constants.html
// https://docs.nvidia.com/deploy/nvml-api/group__nvmlFabricDefs.html
const (
	clockEventReasonHardwareSlowdown           int64 = 0x08
	clockEventReasonHardwareThermalSlowdown    int64 = 0x40
	clockEventReasonHardwarePowerBrakeSlowdown int64 = 0x80

	fabricHealthValueMask = int64(0x03)
	fabricHealthValueTrue = int64(0x01)
)

// fieldsBySignal maps canonical GPU definitions to standard DCGM GPU fields.
// Profiling and CPU entity fields use different DCGM APIs and are
// intentionally absent.
var fieldsBySignal = indexFieldMappings([]fieldMapping{
	dcgmField(gpusignals.PowerDraw, godcgm.DCGM_FI_DEV_BOARD_POWER_WATTS),
	dcgmField(gpusignals.PowerLimitRequested, godcgm.DCGM_FI_DEV_BOARD_POWER_LIMIT_REQUESTED_WATTS),
	dcgmField(gpusignals.PowerLimitEnforced, godcgm.DCGM_FI_DEV_BOARD_POWER_LIMIT_ENFORCED_WATTS),
	dcgmField(gpusignals.TemperatureCelsius, godcgm.DCGM_FI_DEV_GPU_TEMP_CELSIUS),
	dcgmField(gpusignals.PowerViolationDuration, godcgm.DCGM_FI_DEV_POWER_VIOLATION),
	dcgmField(gpusignals.ReliabilityViolationDuration, godcgm.DCGM_FI_DEV_RELIABILITY_VIOLATION),
	dcgmField(gpusignals.BoardLimitViolationDuration, godcgm.DCGM_FI_DEV_BOARD_LIMIT_VIOLATION),
	dcgmField(gpusignals.PerformanceState, godcgm.DCGM_FI_DEV_GPU_PSTATE),
	dcgmField(gpusignals.TemperatureHBMCelsius, godcgm.DCGM_FI_DEV_MEMORY_TEMP_CELSIUS),
	dcgmField(gpusignals.TemperatureThresholdSlowdownCelsius, godcgm.DCGM_FI_DEV_GPU_TEMP_SLOWDOWN_CELSIUS),
	dcgmField(gpusignals.ThermalViolationDuration, godcgm.DCGM_FI_DEV_THERMAL_VIOLATION),
	dcgmField(gpusignals.TemperatureSlowdownMarginCelsius, godcgm.DCGM_FI_DEV_GPU_TEMP_MARGIN_CELSIUS),
	dcgmField(gpusignals.SMClock, godcgm.DCGM_FI_DEV_SM_CLOCK),
	dcgmField(gpusignals.MemoryClock, godcgm.DCGM_FI_DEV_MEM_CLOCK),
	dcgmField(gpusignals.ClockEventReasons, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS),
	dcgmDerivedField(gpusignals.HardwareSlowdownActive, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS,
		bitSet(clockEventReasonHardwareSlowdown)),
	dcgmDerivedField(gpusignals.HardwareThermalSlowdownActive, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS,
		bitSet(clockEventReasonHardwareThermalSlowdown)),
	dcgmDerivedField(gpusignals.HardwarePowerBrakeSlowdownActive, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS,
		bitSet(clockEventReasonHardwarePowerBrakeSlowdown)),
	dcgmField(gpusignals.ComputeUtilization, godcgm.DCGM_FI_DEV_GPU_UTIL_RATIO),
	dcgmField(gpusignals.MemoryCopyUtilization, godcgm.DCGM_FI_DEV_MEM_COPY_UTIL),
	dcgmField(gpusignals.FramebufferFree, godcgm.DCGM_FI_DEV_FB_FREE),
	dcgmField(gpusignals.FramebufferUsed, godcgm.DCGM_FI_DEV_FB_USED),
	dcgmField(gpusignals.FramebufferTotal, godcgm.DCGM_FI_DEV_FB_TOTAL),
	dcgmField(gpusignals.FramebufferUsedRatio, godcgm.DCGM_FI_DEV_FB_USED_RATIO),
	dcgmField(gpusignals.UncorrectableRemappedRows, godcgm.DCGM_FI_DEV_ROW_REMAP_UNCORRECTABLE_TOTAL),
	dcgmField(gpusignals.CorrectableRemappedRows, godcgm.DCGM_FI_DEV_ROW_REMAP_CORRECTABLE_TOTAL),
	dcgmField(gpusignals.RowRemapFailure, godcgm.DCGM_FI_DEV_ROW_REMAP_FAILED),
	dcgmField(gpusignals.RowRemapPending, godcgm.DCGM_FI_DEV_ROW_REMAP_PENDING),
	dcgmField(gpusignals.ECCSingleBitVolatileTotal, godcgm.DCGM_FI_DEV_ECC_SBE_VOL_TOTAL),
	dcgmField(gpusignals.ECCDoubleBitVolatileTotal, godcgm.DCGM_FI_DEV_ECC_DBE_VOL_TOTAL),
	dcgmField(gpusignals.ECCSingleBitAggregateTotal, godcgm.DCGM_FI_DEV_ECC_SBE_AGG_TOTAL),
	dcgmField(gpusignals.ECCDoubleBitAggregateTotal, godcgm.DCGM_FI_DEV_ECC_DBE_AGG_TOTAL),
	dcgmField(gpusignals.ECCSingleBitVolatileDevice, godcgm.DCGM_FI_DEV_ECC_SBE_VOL_DEV),
	dcgmField(gpusignals.ECCDoubleBitVolatileDevice, godcgm.DCGM_FI_DEV_ECC_DBE_VOL_DEV),
	dcgmField(gpusignals.ECCSingleBitAggregateDevice, godcgm.DCGM_FI_DEV_ECC_SBE_AGG_DEV),
	dcgmField(gpusignals.ECCDoubleBitAggregateDevice, godcgm.DCGM_FI_DEV_ECC_DBE_AGG_DEV),
	dcgmField(gpusignals.RemapRowsAvailableHigh, godcgm.DCGM_FI_DEV_BANK_REMAP_AVAIL_HIGH),
	dcgmField(gpusignals.RemapRowsAvailableLow, godcgm.DCGM_FI_DEV_BANK_REMAP_AVAIL_LOW),
	dcgmField(gpusignals.RemapRowsAvailableMax, godcgm.DCGM_FI_DEV_BANK_REMAP_AVAIL_MAX),
	dcgmField(gpusignals.RemapRowsAvailableNone, godcgm.DCGM_FI_DEV_BANK_REMAP_AVAIL_NONE),
	dcgmField(gpusignals.RemapRowsAvailablePartial, godcgm.DCGM_FI_DEV_BANK_REMAP_AVAIL_PARTIAL),
	dcgmField(gpusignals.PCIeReplayCount, godcgm.DCGM_FI_DEV_PCIE_REPLAY_TOTAL),
	dcgmField(gpusignals.NVLinkThroughputTotal, godcgm.DCGM_FI_DEV_NVLINK_THROUGHPUT_TOTAL),
	dcgmField(gpusignals.NVLinkDLCrcErrorCount, godcgm.DCGM_FI_DEV_NVLINK_CRC_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkDLRecoveryErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RECOVERY_TOTAL),
	dcgmField(gpusignals.NVLinkDLReplayErrorCount, godcgm.DCGM_FI_DEV_NVLINK_REPLAY_TOTAL),
	dcgmField(gpusignals.NVLinkRecoverySuccessfulCount, godcgm.DCGM_FI_DEV_NVLINK_RECOVERY_SUCCESSFUL_TOTAL),
	dcgmField(gpusignals.NVLinkRecoveryFailedCount, godcgm.DCGM_FI_DEV_NVLINK_RECOVERY_FAILED_TOTAL),
	dcgmField(gpusignals.FabricManagerStatus, godcgm.DCGM_FI_DEV_FABRIC_MANAGER_STATUS),
	dcgmDerivedField(gpusignals.NVLinkFabricBandwidthDegraded, godcgm.DCGM_FI_DEV_FABRIC_HEALTH_MASK,
		fabricHealthFlag(0)),
	dcgmDerivedField(gpusignals.NVLinkFabricRouteRecoveryActive, godcgm.DCGM_FI_DEV_FABRIC_HEALTH_MASK,
		fabricHealthFlag(2)),
	dcgmDerivedField(gpusignals.NVLinkFabricRouteUnhealthy, godcgm.DCGM_FI_DEV_FABRIC_HEALTH_MASK,
		fabricHealthFlag(4)),
	dcgmDerivedField(gpusignals.NVLinkFabricAccessTimeoutRecovery, godcgm.DCGM_FI_DEV_FABRIC_HEALTH_MASK,
		fabricHealthFlag(6)),
	dcgmField(gpusignals.C2CLinkReplayErrorCount, godcgm.DCGM_FI_DEV_C2C_LINK_REPLAY_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkRXGeneralErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_GENERAL_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkRXErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkRXMalformedPacketErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_PACKET_MALFORMED_TOTAL),
	dcgmField(gpusignals.NVLinkRXRemoteErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_REMOTE_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkRXSymbolErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_SYMBOL_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkRXBufferOverrunErrorCount, godcgm.DCGM_FI_DEV_NVLINK_RX_PACKET_DROPPED_TOTAL),
	dcgmField(gpusignals.NVLinkLocalIntegrityErrorCount, godcgm.DCGM_FI_DEV_NVLINK_INTEGRITY_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkEffectiveErrorCount, godcgm.DCGM_FI_DEV_NVLINK_EFFECTIVE_ERROR_TOTAL),
	dcgmField(gpusignals.NVLinkEffectiveBER, godcgm.DCGM_FI_DEV_NVLINK_EFFECTIVE_BER_RATIO),
	dcgmField(gpusignals.NVLinkSymbolBER, godcgm.DCGM_FI_DEV_NVLINK_SYMBOL_BER_RATIO),
	dcgmField(gpusignals.NVLinkTXDiscardCount, godcgm.DCGM_FI_DEV_NVLINK_TX_DISCARD_TOTAL),
	dcgmField(gpusignals.InforomConfigurationValid, godcgm.DCGM_FI_DEV_INFOROM_VALID),
})

func dcgmField(definition collect.Signal, fieldID godcgm.Short) fieldMapping {
	return fieldMapping{signalID: definition.ID(), fieldID: fieldID}
}

func dcgmDerivedField(definition collect.Signal, fieldID godcgm.Short, transform fieldTransform) fieldMapping {
	return fieldMapping{signalID: definition.ID(), fieldID: fieldID, transform: transform}
}

func indexFieldMappings(mappings []fieldMapping) map[string]fieldMapping {
	indexed := make(map[string]fieldMapping, len(mappings))
	for _, mapping := range mappings {
		indexed[mapping.signalID] = mapping
	}
	return indexed
}

// bitSet turns one bit in a DCGM integer field into a canonical boolean.
func bitSet(mask int64) fieldTransform {
	return func(value *observation.Value) (observation.Value, *observation.CollectionError) {
		integer, failure := int64Field(value)
		if failure != nil {
			return observation.Value{}, failure
		}
		return boolField(integer&mask != 0), nil
	}
}

// Each fabric-health condition occupies two bits: 1 means active, 2 means
// inactive, and 0 means that the condition is not supported. Unsupported is
// preserved as a collection error rather than being reported as healthy.
func fabricHealthFlag(shift uint) fieldTransform {
	return func(value *observation.Value) (observation.Value, *observation.CollectionError) {
		integer, failure := int64Field(value)
		if failure != nil {
			return observation.Value{}, failure
		}
		state := (integer >> shift) & fabricHealthValueMask
		switch state {
		case 0:
			return observation.Value{}, &observation.CollectionError{
				Category: observation.CollectionErrorCategoryUnsupported,
				Detail:   "DCGM fabric health condition is not supported",
			}
		case fabricHealthValueTrue:
			return boolField(true), nil
		case 2:
			return boolField(false), nil
		default:
			return observation.Value{}, parseFieldError(fmt.Sprintf("DCGM fabric health state %d is invalid", state))
		}
	}
}

func int64Field(value *observation.Value) (int64, *observation.CollectionError) {
	integer, ok := value.Kind.(*observation.Value_IntValue)
	if !ok {
		return 0, parseFieldError("derived DCGM signal requires an integer field")
	}
	return integer.IntValue, nil
}

func boolField(value bool) observation.Value {
	return observation.Value{Kind: &observation.Value_BoolValue{BoolValue: value}}
}

func parseFieldError(detail string) *observation.CollectionError {
	return &observation.CollectionError{
		Category: observation.CollectionErrorCategoryParseError,
		Detail:   detail,
	}
}
