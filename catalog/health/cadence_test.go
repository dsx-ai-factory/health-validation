// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"strings"
	"testing"
)

// Continuous is the zero value, so a check that omits SchedulingCadence
// defaults to the most frequent cadence — it runs in every window, including a
// Continuous filter, rather than being silently excluded.
func TestSchedulingCadenceZeroValueIsContinuous(t *testing.T) {
	var zero SchedulingCadence
	if zero != Continuous {
		t.Fatalf("zero value = %v, want Continuous", zero)
	}
	if !zero.Covers(Continuous) {
		t.Errorf("zero-value cadence should cover Continuous")
	}
}

// Cadences nest by frequency: Continuous > Prolog > MaintenanceWindow. A check
// covers a requested cadence when its own cadence is at least as frequent.
func TestSchedulingCadenceCovers(t *testing.T) {
	tests := []struct {
		name      string
		check     SchedulingCadence
		requested SchedulingCadence
		want      bool
	}{
		{"continuous covers continuous", Continuous, Continuous, true},
		{"continuous covers prolog", Continuous, Prolog, true},
		{"continuous covers maintenance", Continuous, MaintenanceWindow, true},
		{"prolog does not cover continuous", Prolog, Continuous, false},
		{"prolog covers prolog", Prolog, Prolog, true},
		{"prolog covers maintenance", Prolog, MaintenanceWindow, true},
		{"maintenance does not cover continuous", MaintenanceWindow, Continuous, false},
		{"maintenance does not cover prolog", MaintenanceWindow, Prolog, false},
		{"maintenance covers maintenance", MaintenanceWindow, MaintenanceWindow, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.check.Covers(tt.requested); got != tt.want {
				t.Errorf("%v.Covers(%v) = %v, want %v", tt.check, tt.requested, got, tt.want)
			}
		})
	}
}

func TestSchedulingCadenceFromName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
		want    SchedulingCadence
	}{
		{name: "continuous", in: "CONTINUOUS", want: Continuous},
		{name: "prolog", in: "PROLOG", want: Prolog},
		{name: "maintenance", in: "MAINTENANCE", want: MaintenanceWindow},
		{name: "lower prolog is not a wire name", in: "prolog", wantErr: "unknown scheduling cadence"},
		{name: "padded maintenance is not a wire name", in: "  MAINTENANCE  ", wantErr: "unknown scheduling cadence"},
		{name: "empty", in: "", wantErr: "unknown scheduling cadence"},
		{name: "unknown", in: "NOT_A_CADENCE", wantErr: "unknown scheduling cadence"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SchedulingCadenceFromName(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("SchedulingCadenceFromName(%q) err = %v, want %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SchedulingCadenceFromName(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("SchedulingCadenceFromName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
