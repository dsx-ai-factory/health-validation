// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

func powerSignals() collect.Signals {
	return collect.Signals{gpusignals.PowerDraw, gpusignals.PowerLimitEnforced, gpusignals.PowerLimitRequested}
}

func measurePower(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool, ret nvml.Return) {
	reads := []struct {
		definition collect.Signal
		read       func() (uint32, nvml.Return)
		operation  string
	}{
		{gpusignals.PowerDraw, dev.GetPowerUsage, "GetPowerUsage"},
		{gpusignals.PowerLimitEnforced, dev.GetEnforcedPowerLimit, "GetEnforcedPowerLimit"},
		{gpusignals.PowerLimitRequested, dev.GetPowerManagementLimit, "GetPowerManagementLimit"},
	}
	for _, read := range reads {
		if ctx.Err() != nil {
			return
		}
		if !want[read.definition.ID()] {
			continue
		}
		category := observation.CollectionErrorCategoryUnavailable
		var value uint32
		if !isGPULost(ret) && !isGPURequiresReset(ret) {
			value, ret = read.read()
			category = categorizeFailure(read.definition, ret)
		}
		if ret != nvml.SUCCESS {
			batch.Observations = append(batch.Observations, errorObservation(now, target, read.definition, read.operation, category, returnName(ret), nvml.ErrorString(ret)))
			continue
		}
		// NVML reports milliwatts; canonical power signals use watts, as DCGM does.
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId: read.definition.ID(), Unit: read.definition.Unit(), Source: source, Target: target, ObservedAt: now,
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: float64(value) / 1000}}},
		})
	}
}
