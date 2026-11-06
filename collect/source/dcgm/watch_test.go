// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"testing"
)

func TestMetricWatchCleanupRetriesUnfinishedSteps(t *testing.T) {
	for _, failingStep := range []string{"unwatch", "destroy"} {
		t.Run(failingStep, func(t *testing.T) {
			failure := errors.New("cleanup failed")
			unwatchCalls, destroyCalls := 0, 0
			watch := metricWatch{
				unwatch: func() error {
					unwatchCalls++
					if failingStep == "unwatch" && unwatchCalls == 1 {
						return failure
					}
					return nil
				},
				destroy: func() error {
					destroyCalls++
					if failingStep == "destroy" && destroyCalls == 1 {
						return failure
					}
					return nil
				},
			}
			if err := watch.close(); !errors.Is(err, failure) {
				t.Fatalf("first cleanup = %v, want failure", err)
			}
			if failingStep == "unwatch" && destroyCalls != 0 {
				t.Fatal("destroyed group before unwatch succeeded")
			}
			if err := watch.close(); err != nil {
				t.Fatalf("retry cleanup = %v", err)
			}
			if err := watch.close(); err != nil {
				t.Fatalf("idempotent cleanup = %v", err)
			}
			wantUnwatch, wantDestroy := 1, 2
			if failingStep == "unwatch" {
				wantUnwatch, wantDestroy = 2, 1
			}
			if unwatchCalls != wantUnwatch || destroyCalls != wantDestroy {
				t.Fatalf("cleanup calls = (%d, %d), want (%d, %d)", unwatchCalls, destroyCalls, wantUnwatch, wantDestroy)
			}
		})
	}
}
