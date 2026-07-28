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
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
)

// NVLinkBackend supplies the per-GPU NVLink measurements consumed by the
// NVLink sensor: one [nvlink.NVLinkMeasurement] per GPU (fabric health, …)
// wrapped in [health.Measurement]. When a device's NVLink state is
// unobservable the implementation should return an [nvlink.ErroredNVLink] for
// it so NVLink checks fail rather than passing silently.
//
// It is one of the three narrow, per-domain backend interfaces ([GPUBackend],
// [NodeBackend]); a full multi-domain source (e.g. the nvml backend)
// implements all three.
type NVLinkBackend interface {
	MeasureNVLink() ([]health.Measurement, error)
}

// MockNVLink returns an [NVLinkBackend] that emits the provided NVLink
// measurement.
//
//	sensor := toolkit.NewNVLinkSensor(toolkit.MockNVLink(
//	    nvlink.NVLinkMeasurement{FabricBandwidthDegraded: true},
//	))
func MockNVLink(m nvlink.NVLinkMeasurement) NVLinkBackend {
	return mockNVLinkBackend{measurements: []health.Measurement{m}}
}

type mockNVLinkBackend struct {
	measurements []health.Measurement
}

func (m mockNVLinkBackend) MeasureNVLink() ([]health.Measurement, error) {
	return m.measurements, nil
}
