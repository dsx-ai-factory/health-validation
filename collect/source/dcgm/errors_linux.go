//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// sentinelType identifies why a watched DCGM field has no usable value.
// Sentinels are collection outcomes, not health verdicts.
type sentinelType int

const (
	sentinelNone sentinelType = iota
	sentinelBlank
	sentinelNotFound
	sentinelNotSupported
	sentinelNotPermissioned
)

// checkSentinel performs type-aware sentinel classification. DCGM sentinel
// representations differ by field type and must be checked before a value is
// interpreted as telemetry.
func checkSentinel(value godcgm.FieldValue_v1) (sentinelType, bool) {
	switch value.FieldType {
	case godcgm.DCGM_FT_INT64:
		switch value.Int64() {
		case godcgm.DCGM_FT_INT64_BLANK:
			return sentinelBlank, true
		case godcgm.DCGM_FT_INT64_NOT_FOUND:
			return sentinelNotFound, true
		case godcgm.DCGM_FT_INT64_NOT_SUPPORTED:
			return sentinelNotSupported, true
		case godcgm.DCGM_FT_INT64_NOT_PERMISSIONED:
			return sentinelNotPermissioned, true
		}
	case godcgm.DCGM_FT_DOUBLE:
		switch value.Float64() {
		case godcgm.DCGM_FT_FP64_BLANK:
			return sentinelBlank, true
		case godcgm.DCGM_FT_FP64_NOT_FOUND:
			return sentinelNotFound, true
		case godcgm.DCGM_FT_FP64_NOT_SUPPORTED:
			return sentinelNotSupported, true
		case godcgm.DCGM_FT_FP64_NOT_PERMISSIONED:
			return sentinelNotPermissioned, true
		}
	case godcgm.DCGM_FT_STRING:
		switch value.String() {
		case godcgm.DCGM_FT_STR_BLANK:
			return sentinelBlank, true
		case godcgm.DCGM_FT_STR_NOT_FOUND:
			return sentinelNotFound, true
		case godcgm.DCGM_FT_STR_NOT_SUPPORTED:
			return sentinelNotSupported, true
		case godcgm.DCGM_FT_STR_NOT_PERMISSIONED:
			return sentinelNotPermissioned, true
		}
	}
	return sentinelNone, false
}

func (sentinel sentinelType) String() string {
	switch sentinel {
	case sentinelBlank:
		return "BLANK (no data yet)"
	case sentinelNotFound:
		return "NOT_FOUND (entity missing)"
	case sentinelNotSupported:
		return "NOT_SUPPORTED (hardware does not support the field)"
	case sentinelNotPermissioned:
		return "NOT_PERMISSIONED (hostengine lacks permission)"
	default:
		return "OK"
	}
}

func (sentinel sentinelType) sourceError() string {
	switch sentinel {
	case sentinelBlank:
		return "DCGM_FT_BLANK"
	case sentinelNotFound:
		return "DCGM_FT_NOT_FOUND"
	case sentinelNotSupported:
		return "DCGM_FT_NOT_SUPPORTED"
	case sentinelNotPermissioned:
		return "DCGM_FT_NOT_PERMISSIONED"
	default:
		return ""
	}
}

// dcgmErrorCode reads a typed go-dcgm status when available and falls back to
// the status embedded in older error strings.
func dcgmErrorCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var dcgmFailure *godcgm.Error
	if errors.As(err, &dcgmFailure) {
		return int(dcgmFailure.Code), true
	}

	matches := dcgmErrorCodePattern.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return 0, false
	}
	parsed, parseErr := strconv.ParseInt(matches[1], 10, 32)
	if parseErr != nil {
		return 0, false
	}
	return int(parsed), true
}

var dcgmErrorCodePattern = regexp.MustCompile(`(?i)error code (-?\d+)`)

// dcgmErrorFromError preserves DCGM status information while an operation
// error passes through collect.Run.
func dcgmErrorFromError(err error) error {
	if code, ok := dcgmErrorCode(err); ok {
		return &dcgmError{
			sourceError: dcgmStatusName(code),
			category:    categoryFromCode(code),
			detail:      err.Error(),
		}
	}
	return &dcgmError{
		category: observation.CollectionErrorCategoryOther,
		detail:   err.Error(),
	}
}

// fieldErrorFromStatus converts one failed DCGM field directly into the
// common collection-error format.
func fieldErrorFromStatus(code int, detail string) observation.CollectionError {
	return observation.CollectionError{
		Category:    categoryFromCode(code),
		SourceError: dcgmStatusName(code),
		Detail:      detail,
	}
}

// fieldErrorFromSentinel converts an unusable field sentinel directly into a
// collection error. A sentinel is a collection outcome, not a health verdict.
func fieldErrorFromSentinel(sentinel sentinelType) observation.CollectionError {
	category := observation.CollectionErrorCategoryOther
	switch sentinel {
	case sentinelBlank, sentinelNotFound:
		category = observation.CollectionErrorCategoryUnavailable
	case sentinelNotSupported:
		category = observation.CollectionErrorCategoryUnsupported
	case sentinelNotPermissioned:
		category = observation.CollectionErrorCategoryPermissionDenied
	}
	return observation.CollectionError{
		Category:    category,
		SourceError: sentinel.sourceError(),
		Detail:      "DCGM field value is " + sentinel.String(),
	}
}

// categoryFromCode maps DCGM status codes to portable collection categories.
func categoryFromCode(code int) observation.CollectionErrorCategory {
	switch code {
	case godcgm.DCGM_ST_TIMEOUT, godcgm.DCGM_ST_NVML_DRIVER_TIMEOUT:
		return observation.CollectionErrorCategoryTimeout
	case godcgm.DCGM_ST_NOT_SUPPORTED, godcgm.DCGM_ST_GPU_NOT_SUPPORTED,
		godcgm.DCGM_ST_FIELD_UNSUPPORTED_BY_API, godcgm.DCGM_ST_PROFILING_NOT_SUPPORTED:
		return observation.CollectionErrorCategoryUnsupported
	case godcgm.DCGM_ST_NO_PERMISSION, godcgm.DCGM_ST_REQUIRES_ROOT:
		return observation.CollectionErrorCategoryPermissionDenied
	case godcgm.DCGM_ST_NOT_CONFIGURED, godcgm.DCGM_ST_NOT_WATCHED,
		godcgm.DCGM_ST_MODULE_NOT_LOADED:
		return observation.CollectionErrorCategoryNotConfigured
	case godcgm.DCGM_ST_INIT_ERROR, godcgm.DCGM_ST_UNINITIALIZED,
		godcgm.DCGM_ST_NVML_ERROR, godcgm.DCGM_ST_CONNECTION_NOT_VALID,
		godcgm.DCGM_ST_LIBRARY_NOT_FOUND, godcgm.DCGM_ST_NVML_NOT_LOADED,
		godcgm.DCGM_ST_NO_DATA, godcgm.DCGM_ST_STALE_DATA:
		return observation.CollectionErrorCategoryUnavailable
	default:
		return observation.CollectionErrorCategoryOther
	}
}

// dcgmStatusName returns the stable DCGM identifier for a status code. Unknown
// codes retain their numeric value so source information is not lost.
func dcgmStatusName(code int) string {
	switch code {
	case godcgm.DCGM_ST_NOT_SUPPORTED:
		return "DCGM_ST_NOT_SUPPORTED"
	case godcgm.DCGM_ST_INIT_ERROR:
		return "DCGM_ST_INIT_ERROR"
	case godcgm.DCGM_ST_NVML_ERROR:
		return "DCGM_ST_NVML_ERROR"
	case godcgm.DCGM_ST_GPU_IS_LOST:
		return "DCGM_ST_GPU_IS_LOST"
	case godcgm.DCGM_ST_RESET_REQUIRED:
		return "DCGM_ST_RESET_REQUIRED"
	case godcgm.DCGM_ST_GPU_NOT_SUPPORTED:
		return "DCGM_ST_GPU_NOT_SUPPORTED"
	case godcgm.DCGM_ST_TIMEOUT:
		return "DCGM_ST_TIMEOUT"
	case godcgm.DCGM_ST_NVML_DRIVER_TIMEOUT:
		return "DCGM_ST_NVML_DRIVER_TIMEOUT"
	case godcgm.DCGM_ST_CONNECTION_NOT_VALID:
		return "DCGM_ST_CONNECTION_NOT_VALID"
	case godcgm.DCGM_ST_NOT_CONFIGURED:
		return "DCGM_ST_NOT_CONFIGURED"
	case godcgm.DCGM_ST_NOT_WATCHED:
		return "DCGM_ST_NOT_WATCHED"
	case godcgm.DCGM_ST_NO_DATA:
		return "DCGM_ST_NO_DATA"
	case godcgm.DCGM_ST_STALE_DATA:
		return "DCGM_ST_STALE_DATA"
	case godcgm.DCGM_ST_NO_PERMISSION:
		return "DCGM_ST_NO_PERMISSION"
	case godcgm.DCGM_ST_REQUIRES_ROOT:
		return "DCGM_ST_REQUIRES_ROOT"
	case godcgm.DCGM_ST_FIELD_UNSUPPORTED_BY_API:
		return "DCGM_ST_FIELD_UNSUPPORTED_BY_API"
	case godcgm.DCGM_ST_PROFILING_NOT_SUPPORTED:
		return "DCGM_ST_PROFILING_NOT_SUPPORTED"
	case godcgm.DCGM_ST_MODULE_NOT_LOADED:
		return "DCGM_ST_MODULE_NOT_LOADED"
	case godcgm.DCGM_ST_UNINITIALIZED:
		return "DCGM_ST_UNINITIALIZED"
	case godcgm.DCGM_ST_LIBRARY_NOT_FOUND:
		return "DCGM_ST_LIBRARY_NOT_FOUND"
	case godcgm.DCGM_ST_NVML_NOT_LOADED:
		return "DCGM_ST_NVML_NOT_LOADED"
	default:
		return fmt.Sprintf("DCGM_STATUS_%d", code)
	}
}
