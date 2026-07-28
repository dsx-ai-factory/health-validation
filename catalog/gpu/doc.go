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

// Package gpu defines the per-GPU measurement type ([GPUMeasurement])
// consumed by GPU-category health checks.
//
// The catalog ships no built-in GPU-category check definitions in this
// package today; XID-driven GPU checks live in [catalog/xid] and
// node-wide checks live in [catalog/node]. Import this package to
// construct or inspect GPU measurements that the sensors produce.
package gpu
