//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestNativeMetricFailuresPreserveSamplesAndStatuses(t *testing.T) {
	for _, status := range []int{
		godcgm.DCGM_ST_CONNECTION_NOT_VALID, godcgm.DCGM_ST_NOT_CONFIGURED,
		godcgm.DCGM_ST_NOT_WATCHED, godcgm.DCGM_ST_UNINITIALIZED,
		godcgm.DCGM_ST_NO_DATA, godcgm.DCGM_ST_STALE_DATA,
		godcgm.DCGM_ST_TIMEOUT, godcgm.DCGM_ST_NO_PERMISSION, godcgm.DCGM_ST_NOT_SUPPORTED,
	} {
		t.Run(dcgmStatusName(status), func(t *testing.T) {
			observedAt := time.Unix(1700000000, 123000000).UTC()
			power := godcgm.FieldValue_v2{
				EntityGroupId: godcgm.FE_GPU, EntityID: 3,
				FieldID:   fieldsBySignal[signalPowerDraw].fieldID,
				FieldType: godcgm.DCGM_FT_DOUBLE, TS: observedAt.UnixMicro(),
			}
			binary.LittleEndian.PutUint64(power.Value[:8], math.Float64bits(125.25))
			failed := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: 3, FieldID: fieldsBySignal[signalTemperature].fieldID, Status: status}
			library := newNative(StandaloneOptions{LiveMetrics: true}, standaloneMode).(*nativeLibrary)
			library.readFields = func([]uint, []godcgm.Short) ([]godcgm.FieldValue_v2, error) {
				return []godcgm.FieldValue_v2{power, failed, {EntityGroupId: godcgm.FE_GPU, EntityID: 3, FieldID: 65535, Status: godcgm.DCGM_ST_CONNECTION_NOT_VALID}}, nil
			}
			values, err := library.ReadGPUMetrics(3, []string{signalPowerDraw, signalTemperature})
			if err != nil {
				t.Fatalf("field failure became an operation error: %v", err)
			}
			if good := values[signalPowerDraw]; good.Value.GetDoubleValue() != 125.25 || !good.ObservedAt.Equal(observedAt) || good.CollectionError != nil {
				t.Fatalf("successful sample was lost: %+v", good)
			}
			if failure := values[signalTemperature].CollectionError; failure.GetSourceError() != dcgmStatusName(status) {
				t.Fatalf("field failure = %v, want status %s", failure, dcgmStatusName(status))
			}
		})
	}
}

func TestWatchRollbackPreservesPrimaryStatusAndBothCauses(t *testing.T) {
	for _, nativeFailure := range []*godcgm.Error{
		{Code: godcgm.DCGM_ST_CONNECTION_NOT_VALID}, {Code: godcgm.DCGM_ST_NOT_CONFIGURED}, {Code: godcgm.DCGM_ST_NOT_WATCHED},
	} {
		for _, statusStep := range []string{"setup", "rollback"} {
			t.Run(dcgmStatusName(int(nativeFailure.Code))+"/"+statusStep, func(t *testing.T) {
				ordinaryFailure := errors.New("ordinary failure")
				setupErr, cleanupErr := error(ordinaryFailure), error(nativeFailure)
				wantStatus := ""
				if statusStep == "setup" {
					setupErr, cleanupErr = nativeFailure, ordinaryFailure
					wantStatus = dcgmStatusName(int(nativeFailure.Code))
				}
				destroys := 0
				cleanup, err := startMetricWatches("test-status", []metricWatchGroup{{fieldIDs: []uint16{100}}}, metricWatchOperations{
					create: func(string, []godcgm.Short) (godcgm.FieldHandle, error) { return godcgm.FieldHandle{}, nil },
					watchEx: func(godcgm.FieldHandle, godcgm.GroupHandle, int64, float64, int32) error {
						return fmt.Errorf("watch: %w", setupErr)
					},
					unwatch: func(godcgm.FieldHandle, godcgm.GroupHandle) error { return nil },
					destroy: func(godcgm.FieldHandle) error {
						destroys++
						if destroys == 1 {
							return fmt.Errorf("rollback: %w", cleanupErr)
						}
						return nil
					},
				})
				if cleanup == nil || err == nil || collectionError(err).SourceError != wantStatus || !errors.Is(err, nativeFailure) || !errors.Is(err, ordinaryFailure) {
					t.Fatalf("rollback changed primary status or lost a cause: cleanup present=%v, error=%v, want status=%q", cleanup != nil, err, wantStatus)
				}
				if err := cleanup(); err != nil || destroys != 2 {
					t.Fatalf("rollback retry = %v, destroy calls = %d", err, destroys)
				}
			})
		}
	}
}

func TestUnwatchNativeTextDoesNotAuthorizeDestruction(t *testing.T) {
	nativeText := errors.New("Field is not being watched")
	destroys := 0
	cleanup, err := startMetricWatches("test-text", []metricWatchGroup{{fieldIDs: []uint16{100}}}, metricWatchOperations{
		create:  func(string, []godcgm.Short) (godcgm.FieldHandle, error) { return godcgm.FieldHandle{}, nil },
		watchEx: func(godcgm.FieldHandle, godcgm.GroupHandle, int64, float64, int32) error { return nil },
		unwatch: func(godcgm.FieldHandle, godcgm.GroupHandle) error { return nativeText },
		destroy: func(godcgm.FieldHandle) error { destroys++; return nil },
	})
	if err != nil || cleanup == nil {
		t.Fatalf("setup: cleanup present=%v, error=%v", cleanup != nil, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		err = cleanup()
		if err == nil || collectionError(err).SourceError != "DCGM_ST_NOT_WATCHED" || !errors.Is(err, nativeText) || destroys != 0 {
			t.Fatalf("untyped absence lost ownership: error=%v, destroy calls=%d", err, destroys)
		}
	}
}
