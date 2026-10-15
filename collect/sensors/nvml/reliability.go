// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func reliabilitySignals() collect.Signals { return gpusignals.Reliability() }

type eccRead struct {
	definition  collect.Signal
	errorType   nvml.MemoryErrorType
	counterType nvml.EccCounterType
	location    nvml.MemoryLocation
	total       bool
}

var eccReads = []eccRead{
	{gpusignals.ECCSingleBitVolatileTotal, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, 0, true},
	{gpusignals.ECCSingleBitVolatileDevice, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY, false},
	{gpusignals.ECCSingleBitVolatileL1Cache, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L1_CACHE, false},
	{gpusignals.ECCSingleBitVolatileL2Cache, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L2_CACHE, false},
	{gpusignals.ECCSingleBitVolatileSRAM, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_SRAM, false},
	{gpusignals.ECCSingleBitVolatileTexture, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY, false},
	{gpusignals.ECCSingleBitVolatileShared, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_SHM, false},
	{gpusignals.ECCSingleBitVolatileRegister, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE, false},
	{gpusignals.ECCDoubleBitVolatileTotal, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, 0, true},
	{gpusignals.ECCDoubleBitVolatileDevice, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY, false},
	{gpusignals.ECCDoubleBitVolatileL1Cache, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L1_CACHE, false},
	{gpusignals.ECCDoubleBitVolatileL2Cache, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L2_CACHE, false},
	{gpusignals.ECCDoubleBitVolatileSRAM, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_SRAM, false},
	{gpusignals.ECCDoubleBitVolatileTexture, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY, false},
	{gpusignals.ECCDoubleBitVolatileShared, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_SHM, false},
	{gpusignals.ECCDoubleBitVolatileRegister, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE, false},
	{gpusignals.ECCSingleBitAggregateTotal, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, 0, true},
	{gpusignals.ECCSingleBitAggregateDevice, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY, false},
	{gpusignals.ECCSingleBitAggregateL1Cache, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE, false},
	{gpusignals.ECCSingleBitAggregateL2Cache, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L2_CACHE, false},
	{gpusignals.ECCSingleBitAggregateSRAM, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_SRAM, false},
	{gpusignals.ECCSingleBitAggregateTexture, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY, false},
	{gpusignals.ECCSingleBitAggregateShared, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_SHM, false},
	{gpusignals.ECCSingleBitAggregateRegister, nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE, false},
	{gpusignals.ECCDoubleBitAggregateTotal, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, 0, true},
	{gpusignals.ECCDoubleBitAggregateDevice, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY, false},
	{gpusignals.ECCDoubleBitAggregateL1Cache, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE, false},
	{gpusignals.ECCDoubleBitAggregateL2Cache, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L2_CACHE, false},
	{gpusignals.ECCDoubleBitAggregateSRAM, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_SRAM, false},
	{gpusignals.ECCDoubleBitAggregateTexture, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY, false},
	{gpusignals.ECCDoubleBitAggregateShared, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_SHM, false},
	{gpusignals.ECCDoubleBitAggregateRegister, nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE, false},
}

// measureReliability keeps native failures local to each counter. Location
// counters require ECC mode; total counters remain readable when ECC is disabled.
func measureReliability(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) {
	r := rawReads{ctx: ctx, batch: batch, now: now, target: target, want: want}
	modeNeeded := want[gpusignals.ECCModeCurrent.ID()] || want[gpusignals.ECCModePending.ID()]
	for _, read := range eccReads {
		if !read.total && want[read.definition.ID()] {
			modeNeeded = true
		}
	}
	current, pending, modeRet := nvml.FEATURE_DISABLED, nvml.FEATURE_DISABLED, nvml.SUCCESS
	if modeNeeded && r.canRead() {
		current, pending, modeRet = dev.GetEccMode()
		r.note(modeRet)
	}
	if modeNeeded {
		r.record(gpusignals.ECCModeCurrent, "GetEccMode", modeRet, rawUint(uint64(current)))
		r.record(gpusignals.ECCModePending, "GetEccMode", modeRet, rawUint(uint64(pending)))
	}
	for _, read := range eccReads {
		if !want[read.definition.ID()] {
			continue
		}
		if !r.canRead() {
			r.record(read.definition, "ECC", nvml.SUCCESS, nil)
			continue
		}
		if !read.total {
			if modeRet != nvml.SUCCESS {
				r.record(read.definition, "GetEccMode", modeRet, nil)
				continue
			}
			if current != nvml.FEATURE_ENABLED {
				r.noValue(read.definition, "ECC_DISABLED", "Location counters require ECC mode to be enabled.")
				continue
			}
		}
		var value uint64
		var ret nvml.Return
		if read.total {
			value, ret = dev.GetTotalEccErrors(read.errorType, read.counterType)
		} else {
			value, ret = dev.GetMemoryErrorCounter(read.errorType, read.counterType, read.location)
		}
		operation := "GetMemoryErrorCounter"
		if read.total {
			operation = "GetTotalEccErrors"
		}
		r.record(read.definition, operation, ret, rawUint(value))
	}
	rows := collect.Signals{gpusignals.CorrectableRemappedRows, gpusignals.UncorrectableRemappedRows, gpusignals.RowRemapPending, gpusignals.RowRemapFailure}
	if r.selected(rows) {
		corr, unc, pendingRows, failure, ret := 0, 0, false, false, nvml.SUCCESS
		if r.canRead() {
			corr, unc, pendingRows, failure, ret = dev.GetRemappedRows()
		}
		values := []*observation.Value{rawUint(uint64(corr)), rawUint(uint64(unc)), rawUint(boolUint(pendingRows)), rawUint(boolUint(failure))}
		for i, definition := range rows {
			if ret == nvml.SUCCESS && r.canRead() && i < 2 && ((i == 0 && corr < 0) || (i == 1 && unc < 0)) {
				if want[definition.ID()] {
					batch.Observations = append(batch.Observations, errorObservation(now, target, definition, "GetRemappedRows", observation.CollectionErrorCategoryParseError, "NEGATIVE_ROW_COUNT", "The native row count is negative."))
				}
				continue
			}
			r.record(definition, "GetRemappedRows", ret, values[i])
		}
	}
	banks := collect.Signals{gpusignals.RemapRowsAvailableMax, gpusignals.RemapRowsAvailableHigh, gpusignals.RemapRowsAvailablePartial, gpusignals.RemapRowsAvailableLow, gpusignals.RemapRowsAvailableNone}
	if r.selected(banks) {
		var histogram nvml.RowRemapperHistogramValues
		ret := nvml.SUCCESS
		if r.canRead() {
			histogram, ret = dev.GetRowRemapperHistogram()
		}
		values := []uint32{histogram.Max, histogram.High, histogram.Partial, histogram.Low, histogram.None}
		for i, definition := range banks {
			r.record(definition, "GetRowRemapperHistogram", ret, rawUint(uint64(values[i])))
		}
	}
}

// rawReads suppresses further native calls after device loss or cancellation.
// The enclosing sensor owns the native lifecycle and reentry gate.
type rawReads struct {
	ctx    context.Context
	batch  *observation.ObservationBatch
	now    *timestamppb.Timestamp
	target *observation.ObservationTarget
	want   map[string]bool
	fatal  nvml.Return
}

func (r *rawReads) canRead() bool { return r.ctx.Err() == nil && r.fatal == nvml.SUCCESS }
func (r *rawReads) selected(definitions collect.Signals) bool {
	for _, definition := range definitions {
		if r.want[definition.ID()] {
			return true
		}
	}
	return false
}

func (r *rawReads) note(ret nvml.Return) {
	if ret == nvml.ERROR_GPU_IS_LOST || ret == nvml.ERROR_RESET_REQUIRED {
		r.fatal = ret
	}
}

func (r *rawReads) record(definition collect.Signal, operation string, ret nvml.Return, value *observation.Value) {
	if !r.want[definition.ID()] {
		return
	}
	if err := r.ctx.Err(); err != nil {
		category := observation.CollectionErrorCategorySourceError
		if err == context.DeadlineExceeded {
			category = observation.CollectionErrorCategoryTimeout
		}
		r.batch.Observations = append(r.batch.Observations, errorObservation(r.now, r.target, definition, operation, category, "", err.Error()))
		return
	}
	if r.fatal != nvml.SUCCESS && ret == nvml.SUCCESS {
		ret = r.fatal
	}
	r.note(ret)
	if ret != nvml.SUCCESS {
		category := categorizeFailure(definition, ret)
		if ret == nvml.ERROR_GPU_IS_LOST || ret == nvml.ERROR_RESET_REQUIRED {
			category = observation.CollectionErrorCategoryUnavailable
		}
		r.batch.Observations = append(r.batch.Observations, errorObservation(r.now, r.target, definition, operation, category, returnName(ret), nvml.ErrorString(ret)))
		return
	}
	r.batch.Observations = append(r.batch.Observations, &observation.Observation{SignalId: definition.ID(), Source: source, Target: r.target, ObservedAt: r.now, Unit: definition.Unit(), Outcome: &observation.Observation_Value{Value: value}})
}

func (r *rawReads) noValue(definition collect.Signal, status, detail string) {
	if !r.want[definition.ID()] {
		return
	}
	r.batch.Observations = append(r.batch.Observations, observation.NewNoValueObservation(definition.ID(), definition.Unit(), source, r.target, r.now, &observation.NoValue{Reason: observation.NoValueReasonNotSupported, SourceStatus: status, Detail: detail}))
}

func rawUint(value uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}
}

func boolUint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}
