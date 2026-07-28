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

package toolkit_test

import (
	"context"
	"testing"

	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestNodeSensor_MockEmits(t *testing.T) {
	sensor := toolkit.NewNodeSensor(toolkit.MockNode(
		node.NodeMeasurement{GPUCount: 8},
	))

	if sensor.Name() != "node" {
		t.Errorf("name = %q, want %q", sensor.Name(), "node")
	}

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	nm, ok := measurements[0].(node.NodeMeasurement)
	if !ok {
		t.Fatal("measurement is not NodeMeasurement")
	}
	if nm.GPUCount != 8 {
		t.Errorf("GPUCount = %d, want 8", nm.GPUCount)
	}
}

func TestNodeSensor_NvidiaSMIStub(t *testing.T) {
	sensor := toolkit.NewNodeSensor(toolkit.NvidiaSMI())
	_, err := sensor.Measure(context.Background())
	if err == nil {
		t.Error("expected error from unimplemented nvidia-smi node backend")
	}
}
