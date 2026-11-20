// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func withAgentSource(a *App, source collect.Sensor) *App {
	return a.WithCollect(source, source.Signals().IDs()...)
}

func agentPowerSignals() collect.Signals { return collect.Signals{gpusignals.PowerDraw} }

func agentNodeCountSource(count int64) collect.Sensor {
	return fixedSensor{id: "test.agent.node", signals: collect.Signals{gpuinventory.GPUCount}, obs: []*observation.Observation{{
		SignalId: gpuinventory.GPUCount.ID(), Source: "test.agent.node", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(), Unit: gpuinventory.GPUCount.Unit(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: count}}},
	}}}
}

type failingAgentNodeSource struct{}

func (failingAgentNodeSource) ID() string { return "test.agent.node.failure" }
func (failingAgentNodeSource) Signals() collect.Signals {
	return collect.Signals{gpuinventory.GPUCount}
}

func (failingAgentNodeSource) Measure(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
	return nil, errors.New("source unavailable")
}
