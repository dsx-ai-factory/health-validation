/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package toolkit

import (
	"context"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
)

// NodeSensor collects host-wide measurements (gpu_count) via a pluggable
// [NodeBackend]. It implements [Sensor] and emits a single
// [node.NodeMeasurement] per Measure call by delegating to the backend's
// [NodeBackend.MeasureNode] method. A full multi-domain source (e.g.
// NVMLBackend) satisfies NodeBackend too, so one source can feed this and the
// GPU/NVLink sensors from a single enumeration.
type NodeSensor struct {
	backend NodeBackend
}

// NewNodeSensor creates a node sensor backed by the given backend.
//
// For production:
//
//	node := toolkit.NewNodeSensor(toolkit.NvidiaSMI())
//
// For testing:
//
//	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
//	    node.NodeMeasurement{GPUCount: 8},
//	))
func NewNodeSensor(backend NodeBackend) *NodeSensor {
	return &NodeSensor{backend: backend}
}

func (s *NodeSensor) Name() string { return "node" }

// Measure does not use the context for the same reason as
// [GPUSensor.Measure]: the count comes from the cgo NVML backend, bounded
// by the caller's watchdog.
func (s *NodeSensor) Measure(_ context.Context) ([]health.Measurement, error) {
	return s.backend.MeasureNode()
}

// Errored reports the node as unobservable so node-level checks
// (e.g. gpu_count) fail Critical when collection could not run.
func (s *NodeSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals...)}
}

// ResourceKey groups this sensor with the others sharing its backend (see
// [GPUSensor.ResourceKey]).
func (s *NodeSensor) ResourceKey() string { return backendResourceKey(s.backend) }
