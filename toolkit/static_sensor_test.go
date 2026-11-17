// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// staticSensor reports fixed measurements under name.
type staticSensor struct {
	name string
	ms   []health.Measurement
}

func (s staticSensor) Name() string { return s.name }
func (s staticSensor) Measure(context.Context) ([]health.Measurement, error) {
	return s.ms, nil
}
func (staticSensor) Errored(error) []health.Measurement { return nil }

func nodeSensor(gpuCount int64) staticSensor {
	return staticSensor{name: "node", ms: []health.Measurement{node.NodeMeasurement{GPUCount: gpuCount}}}
}
