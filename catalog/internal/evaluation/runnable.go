// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package evaluation

import (
	"maps"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// RunnableChecks returns, in ID order, the IDs of the checks that pass
// [checkLevelSkip], the skip [EvaluationEngine.EvaluateBatch] decides from the
// check and evaluation context alone. Dependency skips are deliberately not
// applied: including a dependent that evaluation then skips costs one unneeded
// read, but excluding one that evaluation runs would leave it with no data.
func RunnableChecks(checks map[string]*health.CheckDefinition, paramsFor func(checkID string) Params) []string {
	now := time.Now()
	var runnable []string
	for _, id := range slices.Sorted(maps.Keys(checks)) {
		if _, isSkip := checkLevelSkip(checks[id], paramsFor(id), now); !isSkip {
			runnable = append(runnable, id)
		}
	}
	return runnable
}
