//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
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

func TestDCGMFailureStatusesRetainOriginalCauses(t *testing.T) {
	for _, status := range []struct {
		failure *godcgm.Error
		text    string
	}{
		{failure: &godcgm.Error{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}, text: "Host engine connection invalid/disconnected"},
		{failure: &godcgm.Error{Code: godcgm.DCGM_ST_NOT_CONFIGURED}, text: "Setting not configured"},
		{failure: &godcgm.Error{Code: godcgm.DCGM_ST_NOT_WATCHED}, text: "Field is not being watched"},
	} {
		want := dcgmStatusName(int(status.failure.Code))
		t.Run(want, func(t *testing.T) {
			ordinary := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
			for _, variant := range []struct {
				err         error
				cause       error
				name        string
				sourceError string
				category    observation.CollectionErrorCategory
			}{
				{name: "typed", err: status.failure, cause: status.failure},
				{name: "wrapped typed", err: fmt.Errorf("read: %w", status.failure), cause: status.failure},
				{name: "wrapper text", err: fmt.Errorf("error watching fields: %s", status.text)},
				{
					name: "joined typed", err: errors.Join(ordinary, fmt.Errorf("rollback: %w", status.failure)), cause: status.failure,
					sourceError: "DCGM_ST_NO_DATA", category: observation.CollectionErrorCategoryUnavailable,
				},
				{
					name: "joined uninitialized", err: errors.Join(&godcgm.Error{Code: godcgm.DCGM_ST_UNINITIALIZED}, status.failure), cause: status.failure,
					sourceError: "DCGM_ST_UNINITIALIZED", category: observation.CollectionErrorCategoryUnavailable,
				},
				{
					name: "joined normalized", err: errors.Join(dcgmErrorFromError(ordinary), dcgmErrorFromError(status.failure)), cause: status.failure,
					sourceError: "DCGM_ST_NO_DATA", category: observation.CollectionErrorCategoryUnavailable,
				},
				{
					name: "joined text", err: errors.Join(dcgmErrorFromError(ordinary), errors.New("error watching fields: "+status.text)),
					sourceError: "DCGM_ST_NO_DATA", category: observation.CollectionErrorCategoryUnavailable,
				},
			} {
				t.Run(variant.name, func(t *testing.T) {
					wantSource, wantCategory := want, categoryFromCode(int(status.failure.Code))
					if variant.sourceError != "" {
						wantSource, wantCategory = variant.sourceError, variant.category
					}
					err := dcgmErrorFromError(variant.err)
					failure := collectionError(err)
					if failure.SourceError != wantSource || failure.Category != wantCategory || failure.Detail != variant.err.Error() {
						t.Fatalf("normalized failure = %v, want primary status %s with original detail", &failure, wantSource)
					}
					if !errors.Is(err, variant.err) || (variant.cause != nil && !errors.Is(err, variant.cause)) {
						t.Fatalf("normalization lost original error: %v", err)
					}
					if variant.cause != nil {
						var typed *godcgm.Error
						if !errors.As(err, &typed) {
							t.Fatal("normalization lost typed native error")
						}
					}
				})
			}
		})
	}
}

func TestDCGMStatusNormalizationDoesNotGuessFromOrdinaryErrors(t *testing.T) {
	ordinary := &godcgm.Error{Code: godcgm.DCGM_ST_NO_DATA}
	misleading := fmt.Errorf("Field is not being watched: %w", ordinary)
	if failure := collectionError(dcgmErrorFromError(misleading)); failure.SourceError != "DCGM_ST_NO_DATA" {
		t.Fatalf("typed native status was overridden by text: %v", &failure)
	}
	for _, err := range []error{errors.New("not watched"), errors.New("not configured"), errors.New("connection failed")} {
		if failure := collectionError(dcgmErrorFromError(err)); failure.SourceError != "" {
			t.Fatalf("generic text was treated as native status: %v", &failure)
		}
	}
}
