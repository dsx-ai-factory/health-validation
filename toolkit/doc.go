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

// Package toolkit provides the sensor and backend abstractions partners
// use to feed measurements into the catalog and produce a node verdict.
//
// A [Sensor] collects measurements from a hardware domain — typically
// one [health.Measurement] per observed entity. [Collect] runs one or
// more sensors, hands their measurements to [catalog.Catalog.EvaluateAll],
// and returns the aggregated [health.NodeVerdict].
//
// # GPU sensor
//
// [GPUSensor] wraps a pluggable [GPUBackend] that supplies per-GPU
// measurements. The toolkit ships with two reference backends:
//
//   - [NVMLBackend] — collects GPU state from NVML.
//   - [MockGPU] — emits caller-supplied measurements; used in tests
//     where running real telemetry collection is impractical.
//
// Other backends (DCGM, nvidia-smi, partner-specific) are implemented out of
// tree by satisfying the narrow [GPUBackend] interface — or, for a full
// multi-domain source that also feeds the node and NVLink sensors, all three
// of [GPUBackend], [NodeBackend], and [NVLinkBackend].
//
// # Wiring example
//
//	cat := catalog.New(
//	    catalog.WithArchitecture("GB200"),
//	    catalog.WithExecutionContext(health.OperatorInfra),
//	)
//	gpu := toolkit.NewGPUSensor(nvml.New())
//	nv, err := toolkit.Collect(cat, gpu)
//	if err != nil { /* handle */ }
//	os.Exit(nv.ExitCode())
package toolkit
