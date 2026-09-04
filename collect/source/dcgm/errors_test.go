// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestCollectionError(t *testing.T) {
	tests := []struct {
		err          error
		name         string
		sourceError  string
		wantCategory observation.CollectionErrorCategory
	}{
		{
			name:         "cycle timeout",
			err:          fmt.Errorf("%w: DCGM timed out", collect.ErrCollectionTimeout),
			wantCategory: observation.CollectionErrorCategoryTimeout,
		},
		{
			name:         "canceled",
			err:          context.Canceled,
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{
			name:         "native call in use",
			err:          ErrInUse,
			wantCategory: observation.CollectionErrorCategoryUnavailable,
		},
		{
			name: "typed DCGM error",
			err: &dcgmError{
				sourceError: "DCGM_ST_NOT_SUPPORTED",
				category:    observation.CollectionErrorCategoryUnsupported,
				detail:      "not supported",
			},
			sourceError:  "DCGM_ST_NOT_SUPPORTED",
			wantCategory: observation.CollectionErrorCategoryUnsupported,
		},
		{
			name:         "unknown source error",
			err:          errors.New("unknown DCGM failure"),
			wantCategory: observation.CollectionErrorCategorySourceError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := collectionError(test.err)
			if failure.GetCategory() != test.wantCategory {
				t.Errorf("category = %v, want %v", failure.GetCategory(), test.wantCategory)
			}
			if failure.GetSourceError() != test.sourceError {
				t.Errorf("source error = %q, want %q", failure.GetSourceError(), test.sourceError)
			}
			if failure.GetDetail() != test.err.Error() {
				t.Errorf("detail = %q, want %q", failure.GetDetail(), test.err.Error())
			}
		})
	}
}
