// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package gpu owns the canonical definitions of GPU-domain signals.
//
// A definition does not imply that every GPU sensor supports the signal.
// Sensors declare their own supported subsets using these shared definitions.
package gpu

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/collect"
)

// Fabric Manager status values reported by FabricManagerStatus. These values
// follow DCGM's public dcgmFabricManagerStatus_t contract:
// https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-structs.html
const (
	FabricManagerStatusNotSupported int64 = iota
	FabricManagerStatusNotStarted
	FabricManagerStatusInProgress
	FabricManagerStatusSuccess
	FabricManagerStatusFailure
	FabricManagerStatusUnrecognized
	FabricManagerStatusNVMLTooOld
)

// Canonical GPU signal definitions. Source-specific DCGM and NVML mappings
// live with their sources; overlapping sources reference the same values here.
var (
	PowerDraw                           = collect.NewSignal("gpu.power.draw", "watts")
	PowerLimitRequested                 = collect.NewSignal("gpu.power.limit.requested", "watts")
	PowerLimitEnforced                  = collect.NewSignal("gpu.power.limit.enforced", "watts")
	PowerViolationDuration              = collect.NewSignal("gpu.power.violation.duration", "nanoseconds")
	ReliabilityViolationDuration        = collect.NewSignal("gpu.reliability.violation.duration", "nanoseconds")
	BoardLimitViolationDuration         = collect.NewSignal("gpu.board_limit.violation.duration", "nanoseconds")
	PerformanceState                    = collect.NewSignal("gpu.performance.state", "")
	TemperatureCelsius                  = collect.NewSignal("gpu.temperature_celsius", "celsius")
	TemperatureHBMCelsius               = collect.NewSignal("gpu.temperature_hbm_celsius", "celsius")
	TemperatureSlowdownMarginCelsius    = collect.NewSignal("gpu.temperature_slowdown_margin_celsius", "celsius")
	TemperatureThresholdShutdownCelsius = collect.NewSignal("gpu.temperature_threshold_shutdown_celsius", "celsius")
	TemperatureThresholdSlowdownCelsius = collect.NewSignal("gpu.temperature_threshold_slowdown_celsius", "celsius")
	TemperatureThresholdMemMaxCelsius   = collect.NewSignal("gpu.temperature_threshold_mem_max_celsius", "celsius")
	TemperatureThresholdGPUMaxCelsius   = collect.NewSignal("gpu.temperature_threshold_gpu_max_celsius", "celsius")
	ThermalViolationDuration            = collect.NewSignal("gpu.thermal.violation.duration", "nanoseconds")
	SMClock                             = collect.NewSignal("gpu.clock.sm", "megahertz")
	MemoryClock                         = collect.NewSignal("gpu.clock.memory", "megahertz")
	ClockEventReasons                   = collect.NewSignal("gpu.clock.event_reasons", "1")
	HardwareSlowdownActive              = collect.NewSignal("gpu.clock.hardware_slowdown.active", "")
	HardwareThermalSlowdownActive       = collect.NewSignal("gpu.clock.hardware_thermal_slowdown.active", "")
	HardwarePowerBrakeSlowdownActive    = collect.NewSignal("gpu.clock.hardware_power_brake_slowdown.active", "")
	ComputeUtilization                  = collect.NewSignal("gpu.compute.utilization", "percent")
	MemoryCopyUtilization               = collect.NewSignal("gpu.memory.copy_utilization", "percent")
	FramebufferFree                     = collect.NewSignal("gpu.memory.frame_buffer.free", "mebibytes")
	FramebufferUsed                     = collect.NewSignal("gpu.memory.frame_buffer.used", "mebibytes")
	FramebufferTotal                    = collect.NewSignal("gpu.memory.frame_buffer.total", "mebibytes")
	FramebufferUsedRatio                = collect.NewSignal("gpu.memory.frame_buffer.used_ratio", "1")
	UncorrectableRemappedRows           = collect.NewSignal("gpu.memory.remapped_rows.uncorrectable", "1")
	CorrectableRemappedRows             = collect.NewSignal("gpu.memory.remapped_rows.correctable", "1")
	RowRemapFailure                     = collect.NewSignal("gpu.memory.row_remap.failure", "1")
	RowRemapPending                     = collect.NewSignal("gpu.memory.row_remap.pending", "1")
	ECCSingleBitVolatileTotal           = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.total", "1")
	ECCDoubleBitVolatileTotal           = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.total", "1")
	ECCSingleBitAggregateTotal          = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.total", "1")
	ECCDoubleBitAggregateTotal          = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.total", "1")
	ECCSingleBitVolatileDevice          = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.device", "1")
	ECCDoubleBitVolatileDevice          = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.device", "1")
	ECCSingleBitAggregateDevice         = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.device", "1")
	ECCDoubleBitAggregateDevice         = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.device", "1")
	RemapRowsAvailableHigh              = collect.NewSignal("gpu.memory.remap_rows.available.high", "1")
	RemapRowsAvailableLow               = collect.NewSignal("gpu.memory.remap_rows.available.low", "1")
	RemapRowsAvailableMax               = collect.NewSignal("gpu.memory.remap_rows.available.max", "1")
	RemapRowsAvailableNone              = collect.NewSignal("gpu.memory.remap_rows.available.none", "1")
	RemapRowsAvailablePartial           = collect.NewSignal("gpu.memory.remap_rows.available.partial", "1")
	PCIeReplayCount                     = collect.NewSignal("gpu.pcie.replay.count", "1")
	NVLinkThroughputTotal               = collect.NewSignal("gpu.nvlink.throughput.total", "megabytes_per_second")
	NVLinkDLCrcErrorCount               = collect.NewSignal("gpu.nvlink.error.dl_crc.count", "1")
	NVLinkDLRecoveryErrorCount          = collect.NewSignal("gpu.nvlink.error.dl_recovery.count", "1")
	NVLinkDLReplayErrorCount            = collect.NewSignal("gpu.nvlink.error.dl_replay.count", "1")
	NVLinkRecoverySuccessfulCount       = collect.NewSignal("gpu.nvlink.recovery.successful.count", "1")
	NVLinkRecoveryFailedCount           = collect.NewSignal("gpu.nvlink.recovery.failed.count", "1")
	FabricManagerStatus                 = collect.NewSignal("gpu.fabric_manager.status", "1")
	NVLinkFabricBandwidthDegraded       = collect.NewSignal("gpu.nvlink.fabric.bandwidth.degraded", "")
	NVLinkFabricRouteRecoveryActive     = collect.NewSignal("gpu.nvlink.fabric.route_recovery.active", "")
	NVLinkFabricRouteUnhealthy          = collect.NewSignal("gpu.nvlink.fabric.route.unhealthy", "")
	NVLinkFabricAccessTimeoutRecovery   = collect.NewSignal("gpu.nvlink.fabric.access_timeout_recovery.active", "")
	C2CLinkReplayErrorCount             = collect.NewSignal("gpu.c2c.link.error.replay.count", "1")
	NVLinkRXGeneralErrorCount           = collect.NewSignal("gpu.nvlink.rx.error.general.count", "1")
	NVLinkRXErrorCount                  = collect.NewSignal("gpu.nvlink.rx.error.count", "1")
	NVLinkRXMalformedPacketErrorCount   = collect.NewSignal("gpu.nvlink.rx.error.malformed_packet.count", "1")
	NVLinkRXRemoteErrorCount            = collect.NewSignal("gpu.nvlink.rx.error.remote.count", "1")
	NVLinkRXSymbolErrorCount            = collect.NewSignal("gpu.nvlink.rx.error.symbol.count", "1")
	NVLinkRXBufferOverrunErrorCount     = collect.NewSignal("gpu.nvlink.rx.error.buffer_overrun.count", "1")
	NVLinkLocalIntegrityErrorCount      = collect.NewSignal("gpu.nvlink.local_integrity.error.count", "1")
	NVLinkEffectiveErrorCount           = collect.NewSignal("gpu.nvlink.effective_error.count", "1")
	NVLinkEffectiveBER                  = collect.NewSignal("gpu.nvlink.effective_ber", "1")
	NVLinkSymbolBER                     = collect.NewSignal("gpu.nvlink.symbol_ber", "1")
	NVLinkTXDiscardCount                = collect.NewSignal("gpu.nvlink.tx.discard.count", "1")
	InforomConfigurationValid           = collect.NewSignal("gpu.inforom.configuration.valid", "1")
)

var all = collect.Signals{
	PowerDraw, PowerLimitRequested, PowerLimitEnforced,
	PowerViolationDuration, ReliabilityViolationDuration, BoardLimitViolationDuration,
	PerformanceState, TemperatureCelsius, TemperatureHBMCelsius,
	TemperatureSlowdownMarginCelsius, TemperatureThresholdShutdownCelsius,
	TemperatureThresholdSlowdownCelsius, TemperatureThresholdMemMaxCelsius,
	TemperatureThresholdGPUMaxCelsius, ThermalViolationDuration, SMClock, MemoryClock,
	ClockEventReasons, HardwareSlowdownActive, HardwareThermalSlowdownActive,
	HardwarePowerBrakeSlowdownActive, ComputeUtilization, MemoryCopyUtilization, FramebufferFree,
	FramebufferUsed, FramebufferTotal, FramebufferUsedRatio, UncorrectableRemappedRows,
	CorrectableRemappedRows, RowRemapFailure, RowRemapPending,
	ECCSingleBitVolatileTotal, ECCDoubleBitVolatileTotal, ECCSingleBitAggregateTotal,
	ECCDoubleBitAggregateTotal, ECCSingleBitVolatileDevice,
	ECCDoubleBitVolatileDevice, ECCSingleBitAggregateDevice,
	ECCDoubleBitAggregateDevice, RemapRowsAvailableHigh, RemapRowsAvailableLow,
	RemapRowsAvailableMax, RemapRowsAvailableNone, RemapRowsAvailablePartial,
	PCIeReplayCount, NVLinkThroughputTotal, NVLinkDLCrcErrorCount,
	NVLinkDLRecoveryErrorCount, NVLinkDLReplayErrorCount,
	NVLinkRecoverySuccessfulCount, NVLinkRecoveryFailedCount, FabricManagerStatus,
	NVLinkFabricBandwidthDegraded, NVLinkFabricRouteRecoveryActive,
	NVLinkFabricRouteUnhealthy, NVLinkFabricAccessTimeoutRecovery,
	C2CLinkReplayErrorCount, NVLinkRXGeneralErrorCount, NVLinkRXErrorCount,
	NVLinkRXMalformedPacketErrorCount, NVLinkRXRemoteErrorCount,
	NVLinkRXSymbolErrorCount, NVLinkRXBufferOverrunErrorCount,
	NVLinkLocalIntegrityErrorCount, NVLinkEffectiveErrorCount, NVLinkEffectiveBER,
	NVLinkSymbolBER, NVLinkTXDiscardCount, InforomConfigurationValid,
}

// All returns every canonical GPU definition in stable order.
func All() collect.Signals {
	return slices.Clone(all)
}
