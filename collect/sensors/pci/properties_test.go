// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pci

import (
	"context"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// TestAdvertisedPropertiesAndObservationContract verifies the sensor advertises
// link signals with the expected units and emits well-formed observations.
func TestAdvertisedPropertiesAndObservationContract(t *testing.T) {
	expected := map[string]string{
		"pci.link.speed": "GT/s", "pci.link.speed.max": "GT/s",
		"pci.link.width": "1", "pci.link.width.max": "1", "pci.link.generation": "1", "pci.link.generation.max": "1",
	}
	fixture := fstest.MapFS{}
	for _, address := range []string{"0000:01:00.0", "10000:02:00.0"} {
		for field, value := range map[string]string{
			"vendor": "0x10de", "device": "0x2330", "class": "0x030200",
			"current_link_speed": "16.0 GT/s PCIe", "max_link_speed": "32.0 GT/s PCIe", "current_link_width": "16", "max_link_width": "16",
		} {
			fixture["sys/bus/pci/devices/"+address+"/"+field] = &fstest.MapFile{Data: []byte(value + "\n")}
		}
	}
	sensor := New(fixture)
	if len(sensor.Signals()) != 16 {
		t.Fatalf("advertised %d signals", len(sensor.Signals()))
	}
	for _, signal := range sensor.Signals() {
		unit, ok := expected[signal.ID()]
		if !ok {
			continue
		}
		if signal.Unit() == nil && unit != "" || signal.Unit() != nil && *signal.Unit() != unit {
			t.Fatalf("%s unit = %v, want %q", signal.ID(), signal.Unit(), unit)
		}
		delete(expected, signal.ID())
	}
	if len(expected) != 0 {
		t.Fatalf("missing properties: %v", expected)
	}
	batch, err := sensor.Measure(context.Background())
	if err != nil || len(batch.GetObservations()) != 32 {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	for _, obs := range batch.Observations {
		if obs.Source != ID || obs.ObservedAt == nil || obs.Target.GetPci().GetAddress() == "" {
			t.Fatalf("envelope = %v", obs)
		}
		switch obs.SignalId {
		case "pci.link.speed":
			if obs.GetValue() == nil || obs.GetValue().GetDoubleValue() != 16 {
				t.Fatal(obs)
			}
		case "pci.link.speed.max":
			if obs.GetValue() == nil || obs.GetValue().GetDoubleValue() != 32 {
				t.Fatal(obs)
			}
		case "pci.link.width", "pci.link.width.max":
			if obs.GetValue() == nil || obs.GetValue().GetIntValue() != 16 {
				t.Fatal(obs)
			}
		case "pci.link.generation":
			if obs.GetValue() == nil || obs.GetValue().GetUintValue() != 4 {
				t.Fatal(obs)
			}
		case "pci.link.generation.max":
			if obs.GetValue() == nil || obs.GetValue().GetUintValue() != 5 {
				t.Fatal(obs)
			}
		}
	}
	selected, err := sensor.Measure(context.Background(), collect.WithSignalID("pci.link.width", "pci.link.generation.max"))
	if err != nil || len(selected.GetObservations()) != 4 {
		t.Fatalf("selection = %+v, %v", selected, err)
	}
	want := map[string]int{"pci.link.width": 2, "pci.link.generation.max": 2}
	for _, obs := range selected.GetObservations() {
		if want[obs.SignalId] == 0 {
			t.Fatalf("unexpected or duplicate observation: %v", obs)
		}
		want[obs.SignalId]--
	}
	for signalID, remaining := range want {
		if remaining != 0 {
			t.Fatalf("selection missing %d observations for %s", remaining, signalID)
		}
	}
}

// TestCapturedDeviceLinks checks link observations against representative
// GPU, NIC, bridge, and switch devices from a real PCI capture.
func TestCapturedDeviceLinks(t *testing.T) {
	speed := func(n float64) *observation.Value {
		return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: n}}
	}
	width := func(n int64) *observation.Value {
		return &observation.Value{Kind: &observation.Value_IntValue{IntValue: n}}
	}
	integer := func(n uint64) *observation.Value {
		return &observation.Value{Kind: &observation.Value_UintValue{UintValue: n}}
	}
	// Representative devices from the capture; addresses are synthetic and sysfs
	// text is reconstructed. The switch is a bridge in a branching PCI hierarchy.
	fixtures := []struct {
		attributes map[string]string
		expected   map[string]*observation.Value
		name       string
		address    string
	}{
		{
			name:    "gpu",
			address: "0000:01:00.0",
			attributes: map[string]string{
				"class":              "0x030200\n",
				"current_link_speed": "64.0 GT/s PCIe\n",
				"max_link_speed":     "64.0 GT/s PCIe\n",
				"current_link_width": "16\n",
				"max_link_width":     "16\n",
			},
			expected: map[string]*observation.Value{
				"pci.class.code":          integer(0x030200),
				"pci.link.speed":          speed(64),
				"pci.link.speed.max":      speed(64),
				"pci.link.width":          width(16),
				"pci.link.width.max":      width(16),
				"pci.link.generation":     integer(6),
				"pci.link.generation.max": integer(6),
			},
		},
		{
			name:    "nic",
			address: "0000:02:00.0",
			attributes: map[string]string{
				"class":              "0x020000\n",
				"current_link_speed": "32.0 GT/s PCIe\n",
				"max_link_speed":     "32.0 GT/s PCIe\n",
				"current_link_width": "16\n",
				"max_link_width":     "16\n",
			},
			expected: map[string]*observation.Value{
				"pci.class.code":          integer(0x020000),
				"pci.link.speed":          speed(32),
				"pci.link.speed.max":      speed(32),
				"pci.link.width":          width(16),
				"pci.link.width.max":      width(16),
				"pci.link.generation":     integer(5),
				"pci.link.generation.max": integer(5),
			},
		},
		{
			name:    "bridge",
			address: "0000:03:00.0",
			attributes: map[string]string{
				"class":              "0x060400\n",
				"current_link_speed": "8.0 GT/s PCIe\n",
				"max_link_speed":     "32.0 GT/s PCIe\n",
				"current_link_width": "1\n",
				"max_link_width":     "8\n",
			},
			expected: map[string]*observation.Value{
				"pci.class.code":          integer(0x060400),
				"pci.link.speed":          speed(8),
				"pci.link.speed.max":      speed(32),
				"pci.link.width":          width(1),
				"pci.link.width.max":      width(8),
				"pci.link.generation":     integer(3),
				"pci.link.generation.max": integer(5),
			},
		},
		{
			name:    "switch",
			address: "0000:04:00.0",
			attributes: map[string]string{
				"class":              "0x060400\n",
				"current_link_speed": "64.0 GT/s PCIe\n",
				"max_link_speed":     "64.0 GT/s PCIe\n",
				"current_link_width": "16\n",
				"max_link_width":     "16\n",
			},
			expected: map[string]*observation.Value{
				"pci.class.code":          integer(0x060400),
				"pci.link.speed":          speed(64),
				"pci.link.speed.max":      speed(64),
				"pci.link.width":          width(16),
				"pci.link.width.max":      width(16),
				"pci.link.generation":     integer(6),
				"pci.link.generation.max": integer(6),
			},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			base := "sys/bus/pci/devices/" + fixture.address
			files := fstest.MapFS{base: {Mode: fs.ModeDir | 0o755}}
			for field, value := range fixture.attributes {
				files[base+"/"+field] = &fstest.MapFile{Data: []byte(value)}
			}
			batch, err := New(files).Measure(context.Background(), collect.WithSignalID(
				"pci.class.code", "pci.link.speed", "pci.link.speed.max", "pci.link.width", "pci.link.width.max", "pci.link.generation", "pci.link.generation.max",
			))
			if err != nil {
				t.Fatal(err)
			}
			if validationErr := observation.ValidateBatch(batch); validationErr != nil {
				t.Fatal(validationErr)
			}
			if len(batch.Observations) != len(fixture.expected) {
				t.Fatalf("observations = %d, want %d", len(batch.Observations), len(fixture.expected))
			}
			for _, obs := range batch.Observations {
				want, ok := fixture.expected[obs.SignalId]
				if !ok {
					t.Fatalf("unexpected or duplicate observation: %v", obs)
				}
				delete(fixture.expected, obs.SignalId)
				if obs.Source != ID || obs.ObservedAt == nil || obs.GetTarget().GetPci().GetAddress() != fixture.address {
					t.Fatalf("invalid observation envelope: %v", obs)
				}
				if obs.GetCollectionError() != nil || obs.GetNoValue() != nil || obs.GetValue() == nil || !reflect.DeepEqual(obs.GetValue().Kind, want.Kind) {
					t.Fatalf("%s: got %v, want %v", obs.SignalId, obs, want)
				}
			}
			if len(fixture.expected) != 0 {
				t.Fatalf("missing observations: %v", fixture.expected)
			}
		})
	}
}

func TestIntegratedDeviceLinkWidths(t *testing.T) {
	base := "sys/bus/pci/devices/0000:01:00.0/"
	files := fstest.MapFS{
		base + "current_link_width": {Data: []byte("0\n")},
		base + "max_link_width":     {Data: []byte("255\n")},
	}
	batch, err := New(files).Measure(context.Background(), collect.WithSignalID("pci.link.width", "pci.link.width.max"))
	if err != nil || len(batch.GetObservations()) != 2 {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	for _, obs := range batch.Observations {
		if obs.GetValue() != nil || obs.GetCollectionError() != nil || obs.GetNoValue().GetReason() != observation.NoValueReasonNotSupported {
			t.Fatalf("integrated device width = %v", obs)
		}
	}
}
