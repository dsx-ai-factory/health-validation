// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pci

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
)

type noIO struct{ calls int }

func (f *noIO) Open(string) (fs.File, error) { f.calls++; return nil, fs.ErrPermission }

func TestSelectionBeforeIO(t *testing.T) {
	files := &noIO{}
	s := New(files)
	if batch, err := s.Measure(nil, collect.WithSignalID()); err == nil || batch != nil || files.calls != 0 {
		t.Fatalf("nil context with empty selection = %+v, %v, %d calls", batch, err, files.calls)
	}
	batch, err := s.Measure(context.Background(), collect.WithSignalID())
	if err != nil || len(batch.GetObservations()) != 0 || files.calls != 0 {
		t.Fatalf("empty selection = %+v, %v, %d calls", batch, err, files.calls)
	}
	if _, err := s.Measure(context.Background(), collect.WithSignalID("invalid")); err == nil || files.calls != 0 {
		t.Fatalf("unknown selection = %v, %d calls", err, files.calls)
	}
}

func TestCycleAndIndependentGPUDetection(t *testing.T) {
	files := fstest.MapFS{"sys/bus/pci/devices/0000:01:00.0/class": {Data: []byte("0x030200\n")}, "sys/bus/pci/devices/0000:01:00.0/vendor": {Data: []byte("0x10de\n")}}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cycle, err := collect.NewCycle("pci-cycle", now)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := New(files).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(pcisignals.GPUPresent.ID(), pcisignals.ACSControl.ID()))
	if err != nil || len(batch.GetObservations()) != 2 || batch.CollectionId != "pci-cycle" {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	for _, current := range batch.Observations {
		if !current.ObservedAt.AsTime().Equal(now) || current.Source != ID || current.Target.GetPci().GetAddress() != "0000:01:00.0" {
			t.Fatalf("envelope = %+v", current)
		}
		if current.SignalId == pcisignals.GPUPresent.ID() && !current.GetValue().GetBoolValue() {
			t.Fatal("config failure hid PCI GPU")
		}
		if current.SignalId == pcisignals.ACSControl.ID() && current.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatal("missing config did not surface")
		}
	}
}

func TestNoTargetsHasNoFabricatedTarget(t *testing.T) {
	batch, err := New(fstest.MapFS{"sys/bus/pci/devices": {Mode: fs.ModeDir | 0o755}}).Measure(context.Background(), collect.WithSignalID(pcisignals.Address.ID()))
	if err != nil || len(batch.GetObservations()) != 1 || batch.Observations[0].Target != nil || batch.Observations[0].GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
		t.Fatalf("no PCI targets = %+v, %v", batch, err)
	}
}

func TestGPUPresenceRequiresNVIDIAGPUClass(t *testing.T) {
	for _, test := range []struct {
		vendor string
		class  string
		name   string
		want   bool
	}{
		{name: "ASPEED VGA", vendor: "0x1a03", class: "0x030000", want: false},
		{name: "NVIDIA audio", vendor: "0x10de", class: "0x040300", want: false},
		{name: "NVIDIA VGA", vendor: "0x10de", class: "0x030000", want: true},
		{name: "NVIDIA 3D", vendor: "0x10de", class: "0x030200", want: true},
		{name: "NVIDIA other display", vendor: "0x10de", class: "0x038000", want: false},
		{name: "NVIDIA accelerator", vendor: "0x10de", class: "0x120000", want: true},
		{name: "other accelerator", vendor: "0x8086", class: "0x120000", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fstest.MapFS{
				"sys/bus/pci/devices/0000:01:00.0/class":  {Data: []byte(test.class)},
				"sys/bus/pci/devices/0000:01:00.0/vendor": {Data: []byte(test.vendor)},
			}
			batch, err := New(files).Measure(context.Background(), collect.WithSignalID(pcisignals.GPUPresent.ID()))
			if err != nil || len(batch.GetObservations()) != 1 || batch.Observations[0].GetValue() == nil || batch.Observations[0].GetValue().GetBoolValue() != test.want {
				t.Fatalf("GPU presence = %+v, %v", batch, err)
			}
		})
	}
}

func TestLimitedConfigIsUnavailableNotAbsentACS(t *testing.T) {
	files := fstest.MapFS{"sys/bus/pci/devices/0000:01:00.0/config": {Data: make([]byte, 64)}}
	batch, err := New(files).Measure(context.Background(), collect.WithSignalID(pcisignals.ACSControl.ID()))
	if err != nil || len(batch.GetObservations()) != 1 || batch.Observations[0].GetNoValue() != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("limited configuration = %+v, %v", batch, err)
	}
}
