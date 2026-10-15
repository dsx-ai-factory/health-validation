// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	ECCModeCurrent                = collect.NewSignal("gpu.memory.ecc.mode.current", "1")
	ECCModePending                = collect.NewSignal("gpu.memory.ecc.mode.pending", "1")
	ECCSingleBitVolatileL1Cache   = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.l1_cache", "1")
	ECCDoubleBitVolatileL1Cache   = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.l1_cache", "1")
	ECCSingleBitAggregateL1Cache  = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.l1_cache", "1")
	ECCDoubleBitAggregateL1Cache  = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.l1_cache", "1")
	ECCSingleBitVolatileL2Cache   = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.l2_cache", "1")
	ECCDoubleBitVolatileL2Cache   = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.l2_cache", "1")
	ECCSingleBitAggregateL2Cache  = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.l2_cache", "1")
	ECCDoubleBitAggregateL2Cache  = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.l2_cache", "1")
	ECCSingleBitVolatileSRAM      = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.sram", "1")
	ECCDoubleBitVolatileSRAM      = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.sram", "1")
	ECCSingleBitAggregateSRAM     = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.sram", "1")
	ECCDoubleBitAggregateSRAM     = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.sram", "1")
	ECCSingleBitVolatileTexture   = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.texture", "1")
	ECCDoubleBitVolatileTexture   = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.texture", "1")
	ECCSingleBitAggregateTexture  = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.texture", "1")
	ECCDoubleBitAggregateTexture  = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.texture", "1")
	ECCSingleBitVolatileShared    = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.shared", "1")
	ECCDoubleBitVolatileShared    = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.shared", "1")
	ECCSingleBitAggregateShared   = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.shared", "1")
	ECCDoubleBitAggregateShared   = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.shared", "1")
	ECCSingleBitVolatileRegister  = collect.NewSignal("gpu.memory.ecc.single_bit.volatile.register", "1")
	ECCDoubleBitVolatileRegister  = collect.NewSignal("gpu.memory.ecc.double_bit.volatile.register", "1")
	ECCSingleBitAggregateRegister = collect.NewSignal("gpu.memory.ecc.single_bit.aggregate.register", "1")
	ECCDoubleBitAggregateRegister = collect.NewSignal("gpu.memory.ecc.double_bit.aggregate.register", "1")
)

// ReliabilityAdditions returns the new definitions, without existing canonical counters.
func ReliabilityAdditions() collect.Signals {
	return collect.Signals{ECCModeCurrent, ECCModePending, ECCSingleBitVolatileL1Cache, ECCDoubleBitVolatileL1Cache, ECCSingleBitAggregateL1Cache, ECCDoubleBitAggregateL1Cache, ECCSingleBitVolatileL2Cache, ECCDoubleBitVolatileL2Cache, ECCSingleBitAggregateL2Cache, ECCDoubleBitAggregateL2Cache, ECCSingleBitVolatileSRAM, ECCDoubleBitVolatileSRAM, ECCSingleBitAggregateSRAM, ECCDoubleBitAggregateSRAM, ECCSingleBitVolatileTexture, ECCDoubleBitVolatileTexture, ECCSingleBitAggregateTexture, ECCDoubleBitAggregateTexture, ECCSingleBitVolatileShared, ECCDoubleBitVolatileShared, ECCSingleBitAggregateShared, ECCDoubleBitAggregateShared, ECCSingleBitVolatileRegister, ECCDoubleBitVolatileRegister, ECCSingleBitAggregateRegister, ECCDoubleBitAggregateRegister}
}

// Reliability returns raw ECC and row-remapper signals. DRAM and device memory
// share one NVML location, so the existing device counters represent both.
func Reliability() collect.Signals {
	definitions := ReliabilityAdditions()
	return append(definitions,
		ECCSingleBitVolatileTotal, ECCDoubleBitVolatileTotal, ECCSingleBitAggregateTotal, ECCDoubleBitAggregateTotal,
		ECCSingleBitVolatileDevice, ECCDoubleBitVolatileDevice, ECCSingleBitAggregateDevice, ECCDoubleBitAggregateDevice,
		CorrectableRemappedRows, UncorrectableRemappedRows, RowRemapPending, RowRemapFailure,
		RemapRowsAvailableMax, RemapRowsAvailableHigh, RemapRowsAvailablePartial, RemapRowsAvailableLow, RemapRowsAvailableNone,
	)
}
