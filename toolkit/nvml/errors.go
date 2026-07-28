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

//go:build linux && !android

package nvml

import (
	"fmt"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Error reports a failure from an NVML library call. The backend wraps
// gonvml.Return failures in *Error so consumers can recover both the
// failing operation and the underlying status code by type-asserting
// on the error returned via [health.ErroredMeasurement.Err].
type Error struct {
	// Op is the name of the NVML method that failed (e.g.
	// "DeviceGetUUID", "DeviceGetMemoryErrorCounter"). Use the
	// gonvml method name without the receiver, so consumers can map
	// it back to the NVML C API.
	Op string
	// Return is the gonvml.Return code reported by NVML.
	Return gonvml.Return
}

// Error returns a human-readable message of the form "nvml <Op>: <Return>",
// e.g. "nvml DeviceGetUUID: ERROR_GPU_IS_LOST".
func (e *Error) Error() string {
	return fmt.Sprintf("nvml %s: %s", e.Op, e.Return)
}
