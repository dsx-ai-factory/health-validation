// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func TestResolveSignalsToWatch(t *testing.T) {
	t.Run("defaults to every signal", func(t *testing.T) {
		resolved, err := resolveSignalsToWatch(nil)
		if err != nil {
			t.Fatalf("resolveSignalsToWatch() error = %v", err)
		}
		if !slices.Equal(resolved, supportedSignals) {
			t.Fatalf("resolved signals = %v, want %v", resolved, supportedSignals)
		}
	})

	t.Run("removes duplicates in request order", func(t *testing.T) {
		requested := []string{
			gpusignals.PowerDraw.ID(),
			gpusignals.TemperatureCelsius.ID(),
			gpusignals.PowerDraw.ID(),
		}
		resolved, err := resolveSignalsToWatch(requested)
		if err != nil {
			t.Fatalf("resolveSignalsToWatch() error = %v", err)
		}
		want := collect.Signals{
			gpusignals.PowerDraw,
			gpusignals.TemperatureCelsius,
		}
		if !slices.Equal(resolved, want) {
			t.Fatalf("resolved signals = %v, want %v", resolved, want)
		}
	})

	t.Run("rejects unsupported signal", func(t *testing.T) {
		if _, err := resolveSignalsToWatch([]string{"gpu.unknown"}); err == nil {
			t.Fatal("resolveSignalsToWatch() returned nil error")
		}
	})
}

func TestSensorID(t *testing.T) {
	if got := (&sensor{}).ID(); got != "gpu.dcgm" {
		t.Fatalf("Sensor.ID() = %q, want %q", got, "gpu.dcgm")
	}
}
