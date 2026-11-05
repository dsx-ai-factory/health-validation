// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// dcgmError carries source-specific details through collect.Run. It is used for
// failures of an entire operation, not individual fields.
type dcgmError struct {
	sourceError string
	detail      string
	category    observation.CollectionErrorCategory
}

func (failure *dcgmError) Error() string {
	if failure == nil {
		return ""
	}
	return failure.detail
}

// collectionError converts a cycle or DCGM operation error into the common
// collection-error format.
func collectionError(err error) observation.CollectionError {
	var sourceError string
	var category observation.CollectionErrorCategory
	switch {
	case errors.Is(err, collect.ErrCollectionTimeout), errors.Is(err, context.DeadlineExceeded):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, context.Canceled), errors.Is(err, ErrInUse),
		errors.Is(err, errClosed):
		category = observation.CollectionErrorCategoryUnavailable
	default:
		var failure *dcgmError
		if errors.As(err, &failure) {
			sourceError = failure.sourceError
			category = failure.category
		}
		if category == observation.CollectionErrorCategoryUnspecified {
			category = observation.CollectionErrorCategorySourceError
		}
	}
	return observation.CollectionError{
		Category: category, SourceError: sourceError, Detail: err.Error(),
	}
}
