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
	for _, test := range []struct {
		name            string
		setupFailure    string
		cleanupFailures []string
		wantSetup       []string
		wantFinal       []string
		groupCount      int
	}{
		{
			name: "success", groupCount: 1, wantSetup: []string{"create:0", "watch:0"},
			wantFinal: []string{"create:0", "watch:0", "unwatch:0", "destroy:0"},
		},
		{
			name: "create failure", groupCount: 1, setupFailure: "create:0", wantSetup: []string{"create:0"},
			wantFinal: []string{"create:0"},
		},
		{
			name: "watch failure rolls back", groupCount: 1, setupFailure: "watch:0",
			wantSetup: []string{"create:0", "watch:0", "unwatch:0", "destroy:0"},
			wantFinal: []string{"create:0", "watch:0", "unwatch:0", "destroy:0"},
		},
		{
			name: "rollback attempts both failing cleanup steps", groupCount: 1, setupFailure: "watch:0",
			cleanupFailures: []string{"unwatch:0", "destroy:0"},
			wantSetup:       []string{"create:0", "watch:0", "unwatch:0", "destroy:0"},
			wantFinal:       []string{"create:0", "watch:0", "unwatch:0", "destroy:0"},
		},
		{
			name: "later create failure cleans every created group", groupCount: 3, setupFailure: "create:2",
			cleanupFailures: []string{"unwatch:0", "destroy:1"},
			wantSetup:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "unwatch:0", "destroy:0", "unwatch:1", "destroy:1"},
			wantFinal:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "unwatch:0", "destroy:0", "unwatch:1", "destroy:1"},
		},
		{
			name: "later watch failure includes current group", groupCount: 3, setupFailure: "watch:2",
			cleanupFailures: []string{"unwatch:1", "destroy:2"},
			wantSetup:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "watch:2", "unwatch:0", "destroy:0", "unwatch:1", "destroy:1", "unwatch:2", "destroy:2"},
			wantFinal:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "watch:2", "unwatch:0", "destroy:0", "unwatch:1", "destroy:1", "unwatch:2", "destroy:2"},
		},
		{
			name: "runtime cleanup attempts every step once", groupCount: 3,
			cleanupFailures: []string{"unwatch:0", "destroy:1", "unwatch:2", "destroy:2"},
			wantSetup:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "watch:2"},
			wantFinal:       []string{"create:0", "watch:0", "create:1", "watch:1", "create:2", "watch:2", "unwatch:0", "destroy:0", "unwatch:1", "destroy:1", "unwatch:2", "destroy:2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var operations []string
			failures := make(map[string]error)
			for _, name := range test.cleanupFailures {
				failures[name] = errors.New(name + " failed")
			}
			if test.setupFailure != "" {
				failures[test.setupFailure] = errors.New(test.setupFailure + " failed")
			}
			step := func(operation string, index int) error {
				name := fmt.Sprintf("%s:%d", operation, index)
				operations = append(operations, name)
				return failures[name]
			}
			groups := make([]metricWatchGroup, test.groupCount)
			ownedFieldGroups := make([]godcgm.FieldHandle, test.groupCount)
			for index := range groups {
				groups[index].fieldIDs = []uint16{uint16(100 + index)}
				ownedFieldGroups[index].SetHandle(uintptr(41 + index))
			}
			groupIndex := func(fieldGroup godcgm.FieldHandle) int {
				index := slices.Index(ownedFieldGroups, fieldGroup)
				if index < 0 {
					t.Fatal("operation did not use an owned field group")
				}
				return index
			}
			createCalls := 0
			cleanup, err := startMetricWatches("test-watch", groups, metricWatchOperations{
				create: func(name string, fields []godcgm.Short) (godcgm.FieldHandle, error) {
					index := createCalls
					createCalls++
					if name != "test-watch-fields" || !slices.Equal(fields, []godcgm.Short{godcgm.Short(100 + index)}) {
						t.Fatalf("created unexpected group %q with fields %v", name, fields)
					}
					return ownedFieldGroups[index], step("create", index)
				},
				watchEx: func(fieldGroup godcgm.FieldHandle, group godcgm.GroupHandle, interval int64, maxAge float64, maxSamples int32) error {
					if group != godcgm.GroupAllGPUs() {
						t.Fatal("watch does not use the owned field group and special all-GPU group")
					}
					if interval != 30_000_000 || maxAge != 0 || maxSamples != 1 {
						t.Fatalf("default watch settings = (%d, %v, %d), want (30000000, 0, 1)", interval, maxAge, maxSamples)
					}
					return step("watch", groupIndex(fieldGroup))
				},
				unwatch: func(fieldGroup godcgm.FieldHandle, group godcgm.GroupHandle) error {
					if group != godcgm.GroupAllGPUs() {
						t.Fatal("unwatch does not use the original field and all-GPU groups")
					}
					return step("unwatch", groupIndex(fieldGroup))
				},
				destroy: func(fieldGroup godcgm.FieldHandle) error {
					return step("destroy", groupIndex(fieldGroup))
				},
			})
			wantSetupError := test.setupFailure != ""
			wantCleanup := !wantSetupError
			if (err != nil) != wantSetupError || (cleanup != nil) != wantCleanup {
				t.Fatalf("startMetricWatches() cleanup present = %v, error = %v", cleanup != nil, err)
			}
			if !slices.Equal(operations, test.wantSetup) {
				t.Fatalf("setup operations = %v, want %v", operations, test.wantSetup)
			}
			if cleanup != nil {
				err = cleanup()
				if (err != nil) != (len(test.cleanupFailures) != 0) {
					t.Fatalf("cleanup() = %v, want failures %v", err, test.cleanupFailures)
				}
				if cleanupErr := cleanup(); cleanupErr != nil {
					t.Fatalf("idempotent cleanup: %v", cleanupErr)
				}
			}
			if !slices.Equal(operations, test.wantFinal) {
				t.Fatalf("final operations = %v, want %v", operations, test.wantFinal)
			}
			for name, failure := range failures {
				if !errors.Is(err, failure) {
					t.Errorf("error %v lost cause from %s", err, name)
				}
			}
		})
	}
}

func TestStartMetricWatchesUnwatchErrorsStillDestroy(t *testing.T) {
	for _, test := range []struct {
		failure    error
		name       string
		wantStatus string
	}{
		{name: "typed not watched", failure: &godcgm.Error{Code: godcgm.DCGM_ST_NOT_WATCHED}, wantStatus: "DCGM_ST_NOT_WATCHED"},
		{name: "wrapped typed not watched", failure: fmt.Errorf("native call: %w", &godcgm.Error{Code: godcgm.DCGM_ST_NOT_WATCHED}), wantStatus: "DCGM_ST_NOT_WATCHED"},
		{name: "typed bad parameter", failure: &godcgm.Error{Code: godcgm.DCGM_ST_BADPARAM}, wantStatus: dcgmStatusName(godcgm.DCGM_ST_BADPARAM)},
		{name: "untyped text", failure: errors.New("not watched")},
	} {
		t.Run(test.name, func(t *testing.T) {
			unwatches, destroys := 0, 0
			cleanup, err := startMetricWatches("test-watch", []metricWatchGroup{{fieldIDs: []uint16{uint16(fieldsBySignal[signalPowerDraw].fieldID)}}}, metricWatchOperations{
				create:  func(string, []godcgm.Short) (godcgm.FieldHandle, error) { return godcgm.FieldHandle{}, nil },
				watchEx: func(godcgm.FieldHandle, godcgm.GroupHandle, int64, float64, int32) error { return nil },
				unwatch: func(godcgm.FieldHandle, godcgm.GroupHandle) error { unwatches++; return test.failure },
				destroy: func(godcgm.FieldHandle) error { destroys++; return nil },
			})
			if err != nil || cleanup == nil {
				t.Fatalf("setup: cleanup present=%v, error=%v", cleanup != nil, err)
			}
			err = cleanup()
			if err == nil || !errors.Is(err, test.failure) || collectionError(err).SourceError != test.wantStatus {
				t.Fatalf("cleanup() lost native failure or status: %v, want status %q", err, test.wantStatus)
			}
			if err := cleanup(); err != nil || unwatches != 1 || destroys != 1 {
				t.Fatalf("consumed cleanup = %v, unwatch calls = %d, destroy calls = %d", err, unwatches, destroys)
			}
		})
	}
}
