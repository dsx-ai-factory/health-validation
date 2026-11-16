// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"slices"

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
	gpusignals.HardwareSlowdownActive,
	gpusignals.HardwareThermalSlowdownActive,
	gpusignals.HardwarePowerBrakeSlowdownActive,
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
	gpusignals.NVLinkFabricBandwidthDegraded,
	gpusignals.NVLinkFabricRouteRecoveryActive,
	gpusignals.NVLinkFabricRouteUnhealthy,
	gpusignals.NVLinkFabricAccessTimeoutRecovery,
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

// resolveMetricSignals returns canonical metrics in request order without duplicates.
// Empty requests select every supported metric; health report IDs are rejected.
func resolveMetricSignals(requested []string) (collect.Signals, error) {
	if len(requested) == 0 {
		return slices.Clone(supportedSignals), nil
	}
	byID := make(map[string]collect.Signal, len(supportedSignals))
	for _, definition := range supportedSignals {
		byID[definition.ID()] = definition
	}
	definitions := make(collect.Signals, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, signalID := range requested {
		if _, duplicate := seen[signalID]; duplicate {
			continue
		}
		definition, supported := byID[signalID]
		if !supported {
			return nil, fmt.Errorf("DCGM sensor does not collect metric signal %q", signalID)
		}
		seen[signalID] = struct{}{}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
