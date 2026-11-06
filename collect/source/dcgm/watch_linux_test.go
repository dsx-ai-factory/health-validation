//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestStartMetricWatchesOwnership(t *testing.T) {
	failure := errors.New("native operation failed")
	for _, test := range []struct {
		name        string
		failingStep string
		wantSetup   []string
		wantFinal   []string
		watchFails  bool
		wantCleanup bool
	}{
		{
			name: "success", wantSetup: []string{"create", "watch"},
			wantFinal: []string{"create", "watch", "unwatch", "destroy"}, wantCleanup: true,
		},
		{
			name: "create failure", failingStep: "create", wantSetup: []string{"create"},
			wantFinal: []string{"create"},
		},
		{
			name: "watch failure rolls back", watchFails: true,
			wantSetup: []string{"create", "watch", "unwatch", "destroy"},
			wantFinal: []string{"create", "watch", "unwatch", "destroy"},
		},
		{
			name: "failed rollback retains unwatch", watchFails: true, failingStep: "unwatch",
			wantSetup: []string{"create", "watch", "unwatch"},
			wantFinal: []string{"create", "watch", "unwatch", "unwatch", "destroy"}, wantCleanup: true,
		},
		{
			name: "failed rollback retains destroy", watchFails: true, failingStep: "destroy",
			wantSetup: []string{"create", "watch", "unwatch", "destroy"},
			wantFinal: []string{"create", "watch", "unwatch", "destroy", "destroy"}, wantCleanup: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var operations []string
			var ownedFieldGroup godcgm.FieldHandle
			ownedFieldGroup.SetHandle(41)
			failed := false
			step := func(name string) error {
				operations = append(operations, name)
				if name == test.failingStep && !failed {
					failed = true
					return failure
				}
				return nil
			}
			cleanup, err := startMetricWatches("test-watch", []metricWatchGroup{{fieldIDs: []uint16{uint16(fieldsBySignal[signalPowerDraw].fieldID)}}}, metricWatchOperations{
				create: func(name string, fields []godcgm.Short) (godcgm.FieldHandle, error) {
					if name != "test-watch-fields" || !slices.Equal(fields, []godcgm.Short{fieldsBySignal[signalPowerDraw].fieldID}) {
						t.Fatalf("created unexpected group %q with fields %v", name, fields)
					}
					return ownedFieldGroup, step("create")
				},
				watchEx: func(fieldGroup godcgm.FieldHandle, group godcgm.GroupHandle, interval int64, maxAge float64, maxSamples int32) error {
					if fieldGroup != ownedFieldGroup || group != godcgm.GroupAllGPUs() {
						t.Fatal("watch does not use the owned field group and special all-GPU group")
					}
					if interval != 30_000_000 || maxAge != 0 || maxSamples != 1 {
						t.Fatalf("default watch settings = (%d, %v, %d), want (30000000, 0, 1)", interval, maxAge, maxSamples)
					}
					_ = step("watch")
					if test.watchFails {
						return failure
					}
					return nil
				},
				unwatch: func(fieldGroup godcgm.FieldHandle, group godcgm.GroupHandle) error {
					if fieldGroup != ownedFieldGroup || group != godcgm.GroupAllGPUs() {
						t.Fatal("unwatch does not use the original field and all-GPU groups")
					}
					return step("unwatch")
				},
				destroy: func(fieldGroup godcgm.FieldHandle) error {
					if fieldGroup != ownedFieldGroup {
						t.Fatal("destroy does not use the owned field group")
					}
					return step("destroy")
				},
			})
			wantError := test.watchFails || test.failingStep == "create"
			if (err != nil) != wantError || (cleanup != nil) != test.wantCleanup {
				t.Fatalf("startMetricWatches() cleanup present = %v, error = %v", cleanup != nil, err)
			}
			if !slices.Equal(operations, test.wantSetup) {
				t.Fatalf("setup operations = %v, want %v", operations, test.wantSetup)
			}
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Fatalf("cleanup retry: %v", err)
				}
				if err := cleanup(); err != nil {
					t.Fatalf("idempotent cleanup: %v", err)
				}
			}
			if !slices.Equal(operations, test.wantFinal) {
				t.Fatalf("final operations = %v, want %v", operations, test.wantFinal)
			}
		})
	}
}

func TestStartMetricWatchesUnwatchRequiresConfirmedAbsence(t *testing.T) {
	for _, test := range []struct {
		failure error
		name    string
		absent  bool
	}{
		{name: "typed not watched", failure: &godcgm.Error{Code: godcgm.DCGM_ST_NOT_WATCHED}, absent: true},
		{name: "wrapped typed not watched", failure: fmt.Errorf("native call: %w", &godcgm.Error{Code: godcgm.DCGM_ST_NOT_WATCHED}), absent: true},
		{name: "typed bad parameter", failure: &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}},
		{name: "untyped text", failure: errors.New("not watched")},
	} {
		t.Run(test.name, func(t *testing.T) {
			destroys := 0
			cleanup, err := startMetricWatches("test-watch", []metricWatchGroup{{fieldIDs: []uint16{uint16(fieldsBySignal[signalPowerDraw].fieldID)}}}, metricWatchOperations{
				create:  func(string, []godcgm.Short) (godcgm.FieldHandle, error) { return godcgm.FieldHandle{}, nil },
				watchEx: func(godcgm.FieldHandle, godcgm.GroupHandle, int64, float64, int32) error { return nil },
				unwatch: func(godcgm.FieldHandle, godcgm.GroupHandle) error { return test.failure },
				destroy: func(godcgm.FieldHandle) error { destroys++; return nil },
			})
			if err != nil || cleanup == nil {
				t.Fatalf("setup: cleanup present=%v, error=%v", cleanup != nil, err)
			}
			err = cleanup()
			if (err == nil) != test.absent {
				t.Fatalf("cleanup() = %v, confirmed absent = %v", err, test.absent)
			}
			wantDestroys := 0
			if test.absent {
				wantDestroys = 1
			}
			if destroys != wantDestroys {
				t.Fatalf("destroy calls = %d, want %d", destroys, wantDestroys)
			}
			if !test.absent && !errors.Is(err, test.failure) {
				t.Fatalf("cleanup() lost native failure: %v", err)
			}
		})
	}
}
