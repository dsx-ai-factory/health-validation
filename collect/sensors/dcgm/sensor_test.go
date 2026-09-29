// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
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

func TestDeviceTargetDoesNotLabelSourceIndexAsUUID(t *testing.T) {
	target := deviceTarget(dcgmsource.Device{Index: 7})
	if target.GetGpu().GetUuid() != "" || target.GetGpu().GetSourceLocalId() != "dcgm-index-7" {
		t.Fatalf("device target = %v", target)
	}
}

func TestAppendNoTargetObservations(t *testing.T) {
	sensor := &sensor{source: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})}
	selected := collect.Signals{gpusignals.PowerDraw, gpusignals.TemperatureCelsius}
	batch := &observation.ObservationBatch{}

	sensor.appendNoTargetObservations(batch, selected, time.Now())

	if len(batch.GetObservations()) != len(selected) {
		t.Fatalf("observations = %d, want %d", len(batch.GetObservations()), len(selected))
	}
	for index, current := range batch.GetObservations() {
		if current.GetSignalId() != selected[index].ID() {
			t.Errorf("observation %d signal = %q, want %q", index, current.GetSignalId(), selected[index].ID())
		}
		if current.GetTarget() != nil {
			t.Errorf("observation %d target = %v, want nil", index, current.GetTarget())
		}
		if current.GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
			t.Errorf("observation %d reason = %v, want %v", index, current.GetNoValue().GetReason(), observation.NoValueReasonNoTargets)
		}
	}
}

func TestAppendFieldPreservesNoValue(t *testing.T) {
	sensor := &sensor{source: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})}
	batch := &observation.ObservationBatch{}

	sensor.appendField(
		batch,
		gpusignals.NVLinkFabricRouteUnhealthy,
		observation.GPUTarget("GPU-0"),
		dcgmsource.FieldValue{NoValue: &observation.NoValue{
			Reason:       observation.NoValueReasonNotSupported,
			SourceStatus: "mask condition unsupported",
			Detail:       "fabric condition is not supported",
		}},
		time.Now(),
	)

	if len(batch.GetObservations()) != 1 {
		t.Fatalf("observations = %d, want 1", len(batch.GetObservations()))
	}
	noValue := batch.GetObservations()[0].GetNoValue()
	if noValue.GetReason() != observation.NoValueReasonNotSupported {
		t.Fatalf("reason = %v, want %v", noValue.GetReason(), observation.NoValueReasonNotSupported)
	}
	if noValue.GetSourceStatus() != "mask condition unsupported" {
		t.Errorf("source status = %q, want %q", noValue.GetSourceStatus(), "mask condition unsupported")
	}
}
