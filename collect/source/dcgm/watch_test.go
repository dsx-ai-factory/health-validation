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

func TestMetricWatchesCleanupRetainsOnlyUnfinishedGroups(t *testing.T) {
	unwatchCalls, destroyCalls := [3]int{}, [3]int{}
	watches := make(metricWatches, 0, 3)
	for index := range 3 {
		watches = append(watches, &metricWatch{
			unwatch: func() error {
				unwatchCalls[index]++
				if index == 0 && unwatchCalls[index] == 1 {
					return errors.New("unwatch failed")
				}
				return nil
			},
			destroy: func() error {
				destroyCalls[index]++
				if index == 1 && destroyCalls[index] == 1 {
					return errors.New("destroy failed")
				}
				return nil
			},
		})
	}
	if err := watches.close(); err == nil {
		t.Fatal("cleanup failures were hidden")
	}
	if unwatchCalls != [3]int{1, 1, 1} || destroyCalls != [3]int{0, 1, 1} {
		t.Fatalf("first cleanup: unwatch=%v destroy=%v", unwatchCalls, destroyCalls)
	}
	if err := watches.close(); err != nil {
		t.Fatal(err)
	}
	if err := watches.close(); err != nil {
		t.Fatal(err)
	}
	if unwatchCalls != [3]int{2, 1, 1} || destroyCalls != [3]int{1, 2, 1} {
		t.Fatalf("retry repeated finished work: unwatch=%v destroy=%v", unwatchCalls, destroyCalls)
	}
}
