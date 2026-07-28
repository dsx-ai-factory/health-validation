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

// Package nvml provides a thin client for the NVIDIA Management Library (NVML).
// It wraps go-nvml (github.com/NVIDIA/go-nvml) and manages the NVML session
// lifecycle. Device handles are fetched lazily on first use and cached; a GPU
// whose handle cannot be retrieved is substituted with an errored placeholder
// so a single broken device does not prevent initialization or enumeration.
package nvml
