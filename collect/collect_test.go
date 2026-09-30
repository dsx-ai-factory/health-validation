// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func ExampleWithSignalID() {
	supported := collect.Signals{
		gpusignals.PowerDraw,
		gpusignals.TemperatureCelsius,
	}
	selected, err := collect.NewOp(
		collect.WithSignalID(gpusignals.PowerDraw.ID()),
	).Signals(supported)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(selected.IDs())
	// Output: [gpu.power.draw]
}

func TestOpSignals(t *testing.T) {
	first := collect.NewSignal("gpu.first", "1")
	second := collect.NewSignal("gpu.second", "watts")
	declared := collect.Signals{first, second}

	tests := []struct {
		op          *collect.Op
		name        string
		wantErrText string
		declared    collect.Signals
		want        collect.Signals
	}{
		{
			name:     "defaults to every declared signal",
			op:       collect.NewOp(),
			declared: declared,
			want:     declared,
		},
		{
			name:     "selects in declaration order",
			op:       collect.NewOp(collect.WithSignalID(second.ID(), first.ID())),
			declared: declared,
			want:     declared,
		},
		{
			name:     "selects no signals",
			op:       collect.NewOp(collect.WithSignalID()),
			declared: declared,
		},
		{
			name:        "rejects unknown requested signal",
			op:          collect.NewOp(collect.WithSignalID(second.ID(), "gpu.unknown")),
			declared:    declared,
			wantErrText: "gpu.unknown",
		},
		{
			name:     "deduplicates identical declarations",
			op:       collect.NewOp(),
			declared: collect.Signals{first, first, second},
			want:     declared,
		},
		{
			name:        "rejects conflicting definitions",
			op:          collect.NewOp(),
			declared:    collect.Signals{first, collect.NewSignal(first.ID(), "watts")},
			wantErrText: "conflicting definitions",
		},
		{
			name:        "rejects empty declaration",
			op:          collect.NewOp(),
			declared:    collect.Signals{collect.NewSignal("", "")},
			wantErrText: "empty signal ID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.op.Signals(test.declared)
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("Signals() error = %v, want text %q", err, test.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("Signals() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("Signals() = %v, want %v", got, test.want)
			}
		})
	}
}
