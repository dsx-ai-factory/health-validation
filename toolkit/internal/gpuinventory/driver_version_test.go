// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func withDriver(entry *observation.GPUInventoryEntry, version string) *observation.GPUInventoryEntry {
	entry.Gpu.DriverVersion = &version
	return entry
}

func TestDriverVersion(t *testing.T) {
	for _, tc := range []struct {
		snapshot *observation.GPUInventorySnapshot
		name     string
		want     string
	}{
		{name: "recorded", snapshot: inventory(withDriver(gpuEntry(0, "NVIDIA GB200"), "575.51.03")), want: "575.51.03"},
		{name: "failed slot still carries it", snapshot: inventory(withDriver(failedEntry(0, handleOperation), "570.86.15")), want: "570.86.15"},
		{name: "first entry without it", snapshot: inventory(gpuEntry(0, "NVIDIA GB200"), withDriver(gpuEntry(1, "NVIDIA GB200"), "580.65.06")), want: "580.65.06"},
		{name: "not recorded", snapshot: inventory(gpuEntry(0, "NVIDIA GB200"))},
		{name: "unparseable", snapshot: inventory(withDriver(gpuEntry(0, "NVIDIA GB200"), "r575"))},
		{name: "enumeration failure", snapshot: enumerationFailure()},
		{name: "no GPUs", snapshot: inventory()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DriverVersion(tc.snapshot)
			if ok != (tc.want != "") || got.Raw != tc.want {
				t.Fatalf("DriverVersion = %+v, %v; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestDriverVersionObservation(t *testing.T) {
	queryFailed := gpuEntry(0, "NVIDIA GB200")
	queryFailed.FieldErrors = map[string]*observation.CollectionError{
		"driver_version": {Source: "nvml", Operation: "SystemGetDriverVersion", Category: observation.CollectionErrorCategoryUnavailable, SourceError: "ERROR_UNKNOWN"},
	}
	for _, tc := range []struct {
		snapshot  *observation.GPUInventorySnapshot
		name      string
		want      string
		operation string
	}{
		{name: "recorded", snapshot: inventory(failedEntry(0, handleOperation), withDriver(gpuEntry(1, "NVIDIA GB200"), "575.51.03")), want: "575.51.03"},
		// The raw text is reported as read; the catalog decides whether it parses.
		{name: "unparseable kept", snapshot: inventory(withDriver(gpuEntry(0, "NVIDIA GB200"), "r575")), want: "r575"},
		{name: "query failed", snapshot: inventory(queryFailed), operation: "SystemGetDriverVersion"},
		{name: "not recorded", snapshot: inventory(gpuEntry(0, "NVIDIA GB200")), operation: "SystemGetDriverVersion"},
		{name: "enumeration failure", snapshot: enumerationFailure(), operation: "GetDevices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := measureSignal(t, New(opened(&fakeReader{snapshot: tc.snapshot})), DriverVersionSignal)
			if tc.want != "" {
				if o.GetValue().GetStringValue() != tc.want || o.GetTarget().GetNode() == nil {
					t.Fatalf("observation = %v, want %q on the node", o, tc.want)
				}
				if !proto.Equal(o.GetObservedAt(), tc.snapshot.GetCollectedAt()) {
					t.Fatalf("observed_at not taken from the snapshot: %v", o)
				}
				return
			}
			if got := o.GetCollectionError().GetOperation(); got != tc.operation {
				t.Fatalf("collection error operation = %q, want %q: %v", got, tc.operation, o)
			}
		})
	}
}
