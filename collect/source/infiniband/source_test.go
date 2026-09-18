// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
	"io/fs"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

func TestRead(t *testing.T) {
	files := fstest.MapFS{
		"mlx5_0/ports/1/state":                     {Data: []byte("4: ACTIVE\n")},
		"mlx5_0/ports/1/rate":                      {Data: []byte("2.5 Gb/sec (1X SDR)\n")},
		"mlx5_0/ports/1/counters/port_rcv_data":    {Data: []byte("123\n")},
		"mlx5_0/ports/1/counters/port_xmit_data":   {Data: []byte("18446744073709551615")},
		"mlx5_0/ports/1/hw_counters/out_of_buffer": {Data: []byte("N/A (no PMA)")},
		"mlx5_1/ports/1/state":                     {Data: []byte("bad state")},
	}
	s := New(files)
	signals := collect.Signals{ibsignals.State, ibsignals.Rate, ibsignals.PortRcvData, ibsignals.PortXmitData, ibsignals.HardwareOutOfBuffer}
	readings, err := s.Read(context.Background(), signals)
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 10 {
		t.Fatalf("readings: %v", readings)
	}
	for i, want := range []int64{4, 312500000, 492} {
		if readings[i].Value.GetIntValue() != want || readings[i].Failure != nil {
			t.Fatalf("reading %d: %+v", i, readings[i])
		}
	}
	if readings[3].Failure.GetCategory() != observation.CollectionErrorCategoryParseError || readings[4].Failure.GetCategory() != observation.CollectionErrorCategoryUnsupported || readings[5].Failure.GetCategory() != observation.CollectionErrorCategoryParseError || readings[6].Failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatalf("failure normalization: %+v", readings)
	}
	s = New(files, "mlx5_1")
	readings, err = s.Read(context.Background(), collect.Signals{ibsignals.State})
	if err != nil || len(readings) != 1 {
		t.Fatalf("excluded: %+v / %v", readings, err)
	}
	// Re-enumeration discovers newly added ports without rebuilding the source.
	files["mlx5_0/ports/2/state"] = &fstest.MapFile{Data: []byte("1: DOWN")}
	readings, err = s.Read(context.Background(), collect.Signals{ibsignals.State})
	if err != nil || len(readings) != 2 {
		t.Fatalf("refresh: %+v / %v", readings, err)
	}
}

type restrictedFS struct {
	fs.FS
	reads int
}

func (f *restrictedFS) ReadFile(string) ([]byte, error) { f.reads++; return nil, syscall.EINVAL }

func TestRestrictedFilesAndSelection(t *testing.T) {
	files := &restrictedFS{FS: fstest.MapFS{"mlx5_0/ports/1/state": {}, "mlx5_0/ports/1/counters/link_downed": {}}}
	s := New(files)
	for range 2 {
		readings, err := s.Read(context.Background(), collect.Signals{ibsignals.LinkDowned})
		if err != nil || len(readings) != 1 || readings[0].Failure.GetCategory() != observation.CollectionErrorCategoryUnsupported {
			t.Fatalf("restricted: %+v / %v", readings, err)
		}
	}
	if files.reads != 1 {
		t.Fatalf("read %d times, want once; unselected state must not be read", files.reads)
	}
}

func TestParseBoundaries(t *testing.T) {
	for _, raw := range []string{"NaN Gb/sec", "+Inf Gb/sec", "-1 Gb/sec", "400 bogus", "", "1e100 Gb/sec"} {
		if _, err := parse(ibsignals.Rate, raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"-1", "18446744073709551615", "2305843009213693952", "invalid"} {
		if _, err := parse(ibsignals.PortRcvData, raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := parse(ibsignals.State, "4:"); err == nil {
		t.Error("accepted empty state")
	}
	if len(paths) != len(ibsignals.All()) {
		t.Fatal("signal/source coverage mismatch")
	}
	for _, signal := range ibsignals.All() {
		if paths[signal.ID()] == "" {
			t.Errorf("no source mapping: %s", signal.ID())
		}
	}
}

func TestDeviceInventoryWithoutPorts(t *testing.T) {
	s := New(fstest.MapFS{"mlx5_0/fw_ver": {Data: []byte("28.41.1000\n")}, "mlx5_0/board_id": {Data: []byte("board")}})
	readings, err := s.Read(context.Background(), collect.Signals{ibsignals.FirmwareVersion, ibsignals.BoardID})
	if err != nil || len(readings) != 2 || readings[0].Value.GetStringValue() != "28.41.1000" || readings[0].Entity.GetType() != "infiniband_device" {
		t.Fatalf("inventory: %+v / %v", readings, err)
	}
}

// A consumer's Signal need not be comparable or use the library's concrete type.
type externalSignal []string

func (s externalSignal) ID() string    { return s[0] }
func (s externalSignal) Unit() *string { unit := "wrong"; return &unit }

func TestCanonicalSelection(t *testing.T) {
	s := New(fstest.MapFS{"mlx5_0/ports/1/counters/port_rcv_data": {Data: []byte("10")}})
	definition := externalSignal{ibsignals.PortRcvData.ID()}
	readings, err := s.Read(context.Background(), collect.Signals{definition, definition})
	if err != nil || len(readings) != 1 || readings[0].Value.GetIntValue() != 40 || *readings[0].Signal.Unit() != "bytes" {
		t.Fatalf("canonical reading: %+v / %v", readings, err)
	}
}
