// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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
