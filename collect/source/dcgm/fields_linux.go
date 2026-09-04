//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type fieldMapping struct {
	signalID string
	fieldID  godcgm.Short
}

// fieldsBySignal maps canonical GPU definitions to standard DCGM GPU fields.
// Profiling and CPU entity fields use different DCGM APIs and are
// intentionally absent.
var fieldsBySignal = indexFieldMappings([]fieldMapping{
	dcgmField(gpusignals.PowerDraw, godcgm.DCGM_FI_DEV_POWER_USAGE),
	dcgmField(gpusignals.PowerLimitRequested, godcgm.DCGM_FI_DEV_POWER_MGMT_LIMIT),
	dcgmField(gpusignals.PowerLimitEnforced, godcgm.DCGM_FI_DEV_ENFORCED_POWER_LIMIT),
	dcgmField(gpusignals.TemperatureCelsius, godcgm.DCGM_FI_DEV_GPU_TEMP),
	dcgmField(gpusignals.PowerViolationDuration, godcgm.DCGM_FI_DEV_POWER_VIOLATION),
	dcgmField(gpusignals.ReliabilityViolationDuration, godcgm.DCGM_FI_DEV_RELIABILITY_VIOLATION),
	dcgmField(gpusignals.BoardLimitViolationDuration, godcgm.DCGM_FI_DEV_BOARD_LIMIT_VIOLATION),
	dcgmField(gpusignals.PerformanceState, godcgm.DCGM_FI_DEV_PSTATE),
	dcgmField(gpusignals.TemperatureHBMCelsius, godcgm.DCGM_FI_DEV_MEMORY_TEMP),
	dcgmField(gpusignals.TemperatureThresholdSlowdownCelsius, godcgm.DCGM_FI_DEV_SLOWDOWN_TEMP),
	dcgmField(gpusignals.ThermalViolationDuration, godcgm.DCGM_FI_DEV_THERMAL_VIOLATION),
	dcgmField(gpusignals.TemperatureSlowdownMarginCelsius, godcgm.DCGM_FI_DEV_GPU_TEMP_LIMIT),
	dcgmField(gpusignals.SMClock, godcgm.DCGM_FI_DEV_SM_CLOCK),
	dcgmField(gpusignals.MemoryClock, godcgm.DCGM_FI_DEV_MEM_CLOCK),
	dcgmField(gpusignals.ClockEventReasons, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS),
	dcgmField(gpusignals.ComputeUtilization, godcgm.DCGM_FI_DEV_GPU_UTIL),
	dcgmField(gpusignals.MemoryCopyUtilization, godcgm.DCGM_FI_DEV_MEM_COPY_UTIL),
	dcgmField(gpusignals.FramebufferFree, godcgm.DCGM_FI_DEV_FB_FREE),
	dcgmField(gpusignals.FramebufferUsed, godcgm.DCGM_FI_DEV_FB_USED),
	dcgmField(gpusignals.FramebufferTotal, godcgm.DCGM_FI_DEV_FB_TOTAL),
	dcgmField(gpusignals.FramebufferUsedRatio, godcgm.DCGM_FI_DEV_FB_USED_PERCENT),
	dcgmField(gpusignals.UncorrectableRemappedRows, godcgm.DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS),
	dcgmField(gpusignals.CorrectableRemappedRows, godcgm.DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS),
	dcgmField(gpusignals.RowRemapFailure, godcgm.DCGM_FI_DEV_ROW_REMAP_FAILURE),
	dcgmField(gpusignals.RowRemapPending, godcgm.DCGM_FI_DEV_ROW_REMAP_PENDING),
	dcgmField(gpusignals.ECCSingleBitVolatileTotal, godcgm.DCGM_FI_DEV_ECC_SBE_VOL_TOTAL),
	dcgmField(gpusignals.ECCDoubleBitVolatileTotal, godcgm.DCGM_FI_DEV_ECC_DBE_VOL_TOTAL),
	dcgmField(gpusignals.ECCSingleBitAggregateTotal, godcgm.DCGM_FI_DEV_ECC_SBE_AGG_TOTAL),
	dcgmField(gpusignals.ECCDoubleBitAggregateTotal, godcgm.DCGM_FI_DEV_ECC_DBE_AGG_TOTAL),
	dcgmField(gpusignals.ECCSingleBitVolatileDevice, godcgm.DCGM_FI_DEV_ECC_SBE_VOL_DEV),
	dcgmField(gpusignals.ECCDoubleBitVolatileDevice, godcgm.DCGM_FI_DEV_ECC_DBE_VOL_DEV),
	dcgmField(gpusignals.ECCSingleBitAggregateDevice, godcgm.DCGM_FI_DEV_ECC_SBE_AGG_DEV),
	dcgmField(gpusignals.ECCDoubleBitAggregateDevice, godcgm.DCGM_FI_DEV_ECC_DBE_AGG_DEV),
	dcgmField(gpusignals.RemapRowsAvailableHigh, godcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_HIGH),
	dcgmField(gpusignals.RemapRowsAvailableLow, godcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_LOW),
	dcgmField(gpusignals.RemapRowsAvailableMax, godcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_MAX),
	dcgmField(gpusignals.RemapRowsAvailableNone, godcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_NONE),
	dcgmField(gpusignals.RemapRowsAvailablePartial, godcgm.DCGM_FI_DEV_BANKS_REMAP_ROWS_AVAIL_PARTIAL),
	dcgmField(gpusignals.PCIeReplayCount, godcgm.DCGM_FI_DEV_PCIE_REPLAY_COUNTER),
	// The pinned bindings use the deprecated BANDWIDTH name for throughput field 449.
	dcgmField(gpusignals.NVLinkThroughputTotal, godcgm.DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL),
	dcgmField(gpusignals.NVLinkDLCrcErrorCount, godcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_CRC),
	dcgmField(gpusignals.NVLinkDLRecoveryErrorCount, godcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_RECOVERY),
	dcgmField(gpusignals.NVLinkDLReplayErrorCount, godcgm.DCGM_FI_DEV_NVLINK_ERROR_DL_REPLAY),
	dcgmField(gpusignals.NVLinkRecoverySuccessfulCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_LINK_RECOVERY_SUCCESSFUL_EVENTS),
	dcgmField(gpusignals.NVLinkRecoveryFailedCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS),
	dcgmField(gpusignals.FabricManagerStatus, godcgm.DCGM_FI_DEV_FABRIC_MANAGER_STATUS),
	dcgmField(gpusignals.C2CLinkReplayErrorCount, godcgm.DCGM_FI_DEV_C2C_LINK_ERROR_REPLAY),
	dcgmField(gpusignals.NVLinkRXGeneralErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_GENERAL_ERRORS),
	dcgmField(gpusignals.NVLinkRXErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_ERRORS),
	dcgmField(gpusignals.NVLinkRXMalformedPacketErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_MALFORMED_PACKET_ERRORS),
	dcgmField(gpusignals.NVLinkRXRemoteErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_REMOTE_ERRORS),
	dcgmField(gpusignals.NVLinkRXSymbolErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_SYMBOL_ERRORS),
	dcgmField(gpusignals.NVLinkRXBufferOverrunErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_RX_BUFFER_OVERRUN_ERRORS),
	dcgmField(gpusignals.NVLinkLocalIntegrityErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_LOCAL_LINK_INTEGRITY_ERRORS),
	dcgmField(gpusignals.NVLinkEffectiveErrorCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_EFFECTIVE_ERRORS),
	dcgmField(gpusignals.NVLinkEffectiveBER, godcgm.DCGM_FI_DEV_NVLINK_COUNT_EFFECTIVE_BER_FLOAT),
	dcgmField(gpusignals.NVLinkSymbolBER, godcgm.DCGM_FI_DEV_NVLINK_COUNT_SYMBOL_BER_FLOAT),
	dcgmField(gpusignals.NVLinkTXDiscardCount, godcgm.DCGM_FI_DEV_NVLINK_COUNT_TX_DISCARDS),
	dcgmField(gpusignals.InforomConfigurationValid, godcgm.DCGM_FI_DEV_INFOROM_CONFIG_VALID),
})

func dcgmField(definition collect.Signal, fieldID godcgm.Short) fieldMapping {
	return fieldMapping{signalID: definition.ID(), fieldID: fieldID}
}

func indexFieldMappings(mappings []fieldMapping) map[string]fieldMapping {
	indexed := make(map[string]fieldMapping, len(mappings))
	for _, mapping := range mappings {
		indexed[mapping.signalID] = mapping
	}
	return indexed
}
