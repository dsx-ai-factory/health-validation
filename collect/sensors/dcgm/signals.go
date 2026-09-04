// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"github.com/dsx-ai-factory/health-validation/collect"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// supportedSignals is the subset of canonical GPU signals implemented by the
// DCGM sensor. Native DCGM field IDs remain private to the source.
var supportedSignals = collect.Signals{
	gpusignals.PowerDraw,
	gpusignals.PowerLimitRequested,
	gpusignals.PowerLimitEnforced,
	gpusignals.TemperatureCelsius,
	gpusignals.PowerViolationDuration,
	gpusignals.ReliabilityViolationDuration,
	gpusignals.BoardLimitViolationDuration,
	gpusignals.PerformanceState,
	gpusignals.TemperatureHBMCelsius,
	gpusignals.TemperatureThresholdSlowdownCelsius,
	gpusignals.ThermalViolationDuration,
	gpusignals.TemperatureSlowdownMarginCelsius,
	gpusignals.SMClock,
	gpusignals.MemoryClock,
	gpusignals.ClockEventReasons,
	gpusignals.ComputeUtilization,
	gpusignals.MemoryCopyUtilization,
	gpusignals.FramebufferFree,
	gpusignals.FramebufferUsed,
	gpusignals.FramebufferTotal,
	gpusignals.FramebufferUsedRatio,
	gpusignals.UncorrectableRemappedRows,
	gpusignals.CorrectableRemappedRows,
	gpusignals.RowRemapFailure,
	gpusignals.RowRemapPending,
	gpusignals.ECCSingleBitVolatileTotal,
	gpusignals.ECCDoubleBitVolatileTotal,
	gpusignals.ECCSingleBitAggregateTotal,
	gpusignals.ECCDoubleBitAggregateTotal,
	gpusignals.ECCSingleBitVolatileDevice,
	gpusignals.ECCDoubleBitVolatileDevice,
	gpusignals.ECCSingleBitAggregateDevice,
	gpusignals.ECCDoubleBitAggregateDevice,
	gpusignals.RemapRowsAvailableHigh,
	gpusignals.RemapRowsAvailableLow,
	gpusignals.RemapRowsAvailableMax,
	gpusignals.RemapRowsAvailableNone,
	gpusignals.RemapRowsAvailablePartial,
	gpusignals.PCIeReplayCount,
	gpusignals.NVLinkThroughputTotal,
	gpusignals.NVLinkDLCrcErrorCount,
	gpusignals.NVLinkDLRecoveryErrorCount,
	gpusignals.NVLinkDLReplayErrorCount,
	gpusignals.NVLinkRecoverySuccessfulCount,
	gpusignals.NVLinkRecoveryFailedCount,
	gpusignals.FabricManagerStatus,
	gpusignals.C2CLinkReplayErrorCount,
	gpusignals.NVLinkRXGeneralErrorCount,
	gpusignals.NVLinkRXErrorCount,
	gpusignals.NVLinkRXMalformedPacketErrorCount,
	gpusignals.NVLinkRXRemoteErrorCount,
	gpusignals.NVLinkRXSymbolErrorCount,
	gpusignals.NVLinkRXBufferOverrunErrorCount,
	gpusignals.NVLinkLocalIntegrityErrorCount,
	gpusignals.NVLinkEffectiveErrorCount,
	gpusignals.NVLinkEffectiveBER,
	gpusignals.NVLinkSymbolBER,
	gpusignals.NVLinkTXDiscardCount,
	gpusignals.InforomConfigurationValid,
}

var supportedByID = func() map[string]collect.Signal {
	indexed := make(map[string]collect.Signal, len(supportedSignals))
	for _, current := range supportedSignals {
		indexed[current.ID()] = current
	}
	return indexed
}()
