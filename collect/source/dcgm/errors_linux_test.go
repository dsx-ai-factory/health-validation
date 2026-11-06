//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestFieldErrorFromSentinel(t *testing.T) {
	tests := []struct {
		name         string
		wantSource   string
		sentinel     sentinelType
		wantCategory observation.CollectionErrorCategory
	}{
		{
			name:         "blank",
			sentinel:     sentinelBlank,
			wantCategory: observation.CollectionErrorCategoryUnavailable,
			wantSource:   "DCGM_FT_BLANK",
		},
		{
			name:         "not found",
			sentinel:     sentinelNotFound,
			wantCategory: observation.CollectionErrorCategoryUnavailable,
			wantSource:   "DCGM_FT_NOT_FOUND",
		},
		{
			name:         "not supported",
			sentinel:     sentinelNotSupported,
			wantCategory: observation.CollectionErrorCategoryUnsupported,
			wantSource:   "DCGM_FT_NOT_SUPPORTED",
		},
		{
			name:         "not permissioned",
			sentinel:     sentinelNotPermissioned,
			wantCategory: observation.CollectionErrorCategoryPermissionDenied,
			wantSource:   "DCGM_FT_NOT_PERMISSIONED",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := fieldErrorFromSentinel(test.sentinel)
			if failure.GetCategory() != test.wantCategory {
				t.Errorf("category = %v, want %v", failure.GetCategory(), test.wantCategory)
			}
			if failure.GetSourceError() != test.wantSource {
				t.Errorf("source error = %q, want %q", failure.GetSourceError(), test.wantSource)
			}
		})
	}
}

func TestCategoryFromCodeNoData(t *testing.T) {
	for _, code := range []int{godcgm.DCGM_ST_NO_DATA, godcgm.DCGM_ST_STALE_DATA} {
		if got := categoryFromCode(code); got != observation.CollectionErrorCategoryUnavailable {
			t.Errorf("categoryFromCode(%d) = %v, want %v", code, got, observation.CollectionErrorCategoryUnavailable)
		}
	}
}

func TestDCGMErrorFromStatusText(t *testing.T) {
	tests := []struct {
		err          string
		wantSource   string
		wantCategory observation.CollectionErrorCategory
	}{
		{
			err:          "error connecting to nv-hostengine: Host engine connection invalid/disconnected",
			wantSource:   "DCGM_ST_CONNECTION_NOT_VALID",
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{
			err:          "error getting entity count: Host engine connection invalid/disconnected",
			wantSource:   "DCGM_ST_CONNECTION_NOT_VALID",
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{
			err:          "error initializing DCGM: DCGM initialization error",
			wantSource:   "DCGM_ST_INIT_ERROR",
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{
			err:          "error starting nv-hostengine: DCGM initialization error",
			wantSource:   "DCGM_ST_INIT_ERROR",
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{err: "libdcgm.so.4 not found", wantCategory: observation.CollectionErrorCategoryOther},
	}
	for _, test := range tests {
		var failure *dcgmError
		if !errors.As(dcgmErrorFromError(errors.New(test.err)), &failure) {
			t.Fatalf("dcgmErrorFromError(%q) is not a *dcgmError", test.err)
		}
		if failure.sourceError != test.wantSource || failure.category != test.wantCategory || failure.detail != test.err {
			t.Errorf("dcgmErrorFromError(%q) = %q %v %q, want %q %v", test.err,
				failure.sourceError, failure.category, failure.detail, test.wantSource, test.wantCategory)
		}
	}
}
