//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
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
