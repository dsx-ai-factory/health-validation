// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

const source = "nvml"

// errorObservation builds the collection-error observation for one failed
// signal read. The native error name survives next to the normalized
// category; there are no sentinel values.
func errorObservation(now *timestamppb.Timestamp, target *observation.ObservationTarget, definition collect.Signal, operation string, category observation.CollectionErrorCategory, sourceError, detail string) *observation.Observation {
	failure := observation.CollectionError{
		Source:      source,
		Operation:   operation,
		Category:    category,
		SourceError: sourceError,
		Detail:      detail,
	}
	return observation.NewCollectionErrorObservation(
		definition.ID(), definition.Unit(), source, operation, target, now, &failure,
	)
}

// returnName returns the stable NVML identifier for a return value. Unknown
// values retain their number so source information is not lost.
func returnName(ret nvml.Return) string {
	switch ret {
	case nvml.SUCCESS:
		return "SUCCESS"
	case nvml.ERROR_UNINITIALIZED:
		return "ERROR_UNINITIALIZED"
	case nvml.ERROR_INVALID_ARGUMENT:
		return "ERROR_INVALID_ARGUMENT"
	case nvml.ERROR_NOT_SUPPORTED:
		return "ERROR_NOT_SUPPORTED"
	case nvml.ERROR_NO_PERMISSION:
		return "ERROR_NO_PERMISSION"
	case nvml.ERROR_ALREADY_INITIALIZED:
		return "ERROR_ALREADY_INITIALIZED"
	case nvml.ERROR_NOT_FOUND:
		return "ERROR_NOT_FOUND"
	case nvml.ERROR_INSUFFICIENT_SIZE:
		return "ERROR_INSUFFICIENT_SIZE"
	case nvml.ERROR_INSUFFICIENT_POWER:
		return "ERROR_INSUFFICIENT_POWER"
	case nvml.ERROR_DRIVER_NOT_LOADED:
		return "ERROR_DRIVER_NOT_LOADED"
	case nvml.ERROR_TIMEOUT:
		return "ERROR_TIMEOUT"
	case nvml.ERROR_IRQ_ISSUE:
		return "ERROR_IRQ_ISSUE"
	case nvml.ERROR_LIBRARY_NOT_FOUND:
		return "ERROR_LIBRARY_NOT_FOUND"
	case nvml.ERROR_FUNCTION_NOT_FOUND:
		return "ERROR_FUNCTION_NOT_FOUND"
	case nvml.ERROR_CORRUPTED_INFOROM:
		return "ERROR_CORRUPTED_INFOROM"
	case nvml.ERROR_GPU_IS_LOST:
		return "ERROR_GPU_IS_LOST"
	case nvml.ERROR_RESET_REQUIRED:
		return "ERROR_RESET_REQUIRED"
	case nvml.ERROR_OPERATING_SYSTEM:
		return "ERROR_OPERATING_SYSTEM"
	case nvml.ERROR_LIB_RM_VERSION_MISMATCH:
		return "ERROR_LIB_RM_VERSION_MISMATCH"
	case nvml.ERROR_IN_USE:
		return "ERROR_IN_USE"
	case nvml.ERROR_MEMORY:
		return "ERROR_MEMORY"
	case nvml.ERROR_NO_DATA:
		return "ERROR_NO_DATA"
	case nvml.ERROR_VGPU_ECC_NOT_SUPPORTED:
		return "ERROR_VGPU_ECC_NOT_SUPPORTED"
	case nvml.ERROR_INSUFFICIENT_RESOURCES:
		return "ERROR_INSUFFICIENT_RESOURCES"
	case nvml.ERROR_FREQ_NOT_SUPPORTED:
		return "ERROR_FREQ_NOT_SUPPORTED"
	case nvml.ERROR_ARGUMENT_VERSION_MISMATCH:
		return "ERROR_ARGUMENT_VERSION_MISMATCH"
	case nvml.ERROR_DEPRECATED:
		return "ERROR_DEPRECATED"
	case nvml.ERROR_NOT_READY:
		return "ERROR_NOT_READY"
	case nvml.ERROR_GPU_NOT_FOUND:
		return "ERROR_GPU_NOT_FOUND"
	case nvml.ERROR_INVALID_STATE:
		return "ERROR_INVALID_STATE"
	case nvml.ERROR_RESET_TYPE_NOT_SUPPORTED:
		return "ERROR_RESET_TYPE_NOT_SUPPORTED"
	case nvml.ERROR_UNKNOWN:
		return "ERROR_UNKNOWN"
	default:
		return fmt.Sprintf("NVML_RETURN_%d", ret)
	}
}

// isGPULost returns true if the return indicates the GPU is lost — likely
// physically removed, or Xid 79 (GPU has fallen off the bus).
// ref. https://github.com/leptonai/gpud/issues/604
// Ported from gpud pkg/nvidia/errors; the sensor reports the match as a
// CollectionError in collect/observation.
func isGPULost(ret nvml.Return) bool {
	if ret == nvml.ERROR_GPU_IS_LOST {
		return true
	}
	e := strings.ToLower(strings.TrimSpace(nvml.ErrorString(ret)))
	return strings.Contains(e, "gpu lost") || strings.Contains(e, "gpu is lost") || strings.Contains(e, "gpu_is_lost")
}

// isGPURequiresReset returns true if the return indicates the GPU requires
// reset (e.g. NVML reports "GPU requires reset").
func isGPURequiresReset(ret nvml.Return) bool {
	if ret == nvml.ERROR_RESET_REQUIRED {
		return true
	}
	e := strings.ToLower(strings.TrimSpace(nvml.ErrorString(ret)))
	return strings.Contains(e, "gpu requires reset") || strings.Contains(e, "gpu reset")
}
