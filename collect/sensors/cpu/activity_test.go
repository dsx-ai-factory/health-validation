// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
)

func TestActivityAccountingAndCycle(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/stat":    {Data: []byte("cpu 101 202 303 404 505 606 707 808 909 1001\ncpu0 0 0 0 0\n")},
		"proc/loadavg": {Data: []byte("1.25 2.5 3.75 2/8 123\n")},
	}
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cycle, err := collect.NewCycle("activity", started)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, definition := range cpusignals.Activity() {
		ids = append(ids, definition.ID())
	}
	batch, err := New(fsys).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ids...))
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	want := []float64{1.01, 2.02, 3.03, 4.04, 5.05, 6.06, 7.07, 8.08, 9.09, 10.01, 1.25, 2.5, 3.75}
	if len(batch.Observations) != len(want) {
		t.Fatalf("observations = %d", len(batch.Observations))
	}
	for i, o := range batch.Observations {
		if o.GetValue().GetDoubleValue() != want[i] || o.GetCollectionError() != nil {
			t.Errorf("%s = %v", o.SignalId, o)
		}
		if !o.ObservedAt.AsTime().Equal(started) {
			t.Errorf("timestamp = %v", o.ObservedAt)
		}
	}
}

func TestActivityIndependentParseAndSelection(t *testing.T) {
	fsys := fstest.MapFS{"proc/stat": {Data: []byte("cpu 100 broken 300 400 broken\n")}, "proc/loadavg": {Data: []byte("NaN 2 3\n")}}
	got := measure(t, fsys, collect.WithSignalID(cpusignals.UserSeconds.ID(), cpusignals.NiceSeconds.ID(), cpusignals.SystemSeconds.ID(), cpusignals.IOWaitSeconds.ID(), cpusignals.GuestSeconds.ID(), cpusignals.Load1.ID(), cpusignals.Load5.ID()))
	for _, id := range []string{cpusignals.UserSeconds.ID(), cpusignals.SystemSeconds.ID(), cpusignals.Load5.ID()} {
		if got[id].GetCollectionError() != nil {
			t.Errorf("%s failed", id)
		}
	}
	for _, id := range []string{cpusignals.NiceSeconds.ID(), cpusignals.IOWaitSeconds.ID(), cpusignals.Load1.ID()} {
		if got[id].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Errorf("%s = %v", id, got[id])
		}
	}
	if got[cpusignals.GuestSeconds.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnsupported {
		t.Fatal("missing guest field was not unsupported")
	}
	if got := measure(t, fstest.MapFS{}, collect.WithSignalID()); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestActivityStatFieldBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want []observation.CollectionErrorCategory
	}{
		{"missing aggregate", "cpu0 100 200 300 400\n", []observation.CollectionErrorCategory{
			observation.CollectionErrorCategoryParseError, observation.CollectionErrorCategoryParseError,
			observation.CollectionErrorCategoryParseError, observation.CollectionErrorCategoryParseError,
		}},
		{"truncated mandatory", "cpu 100 200 300\n", []observation.CollectionErrorCategory{
			observation.CollectionErrorCategoryParseError, observation.CollectionErrorCategoryParseError,
			observation.CollectionErrorCategoryParseError, observation.CollectionErrorCategoryParseError,
		}},
		{"original four", "cpu 100 200 300 400\n", []observation.CollectionErrorCategory{
			observation.CollectionErrorCategoryUnsupported, observation.CollectionErrorCategoryUnsupported, 0, 0,
		}},
		{"iowait appended", "cpu 100 200 300 400 500\n", []observation.CollectionErrorCategory{
			observation.CollectionErrorCategoryUnsupported, 0, 0, 0,
		}},
		{"guest nice absent", "cpu 100 200 300 400 500 600 700 800 900\n", []observation.CollectionErrorCategory{
			observation.CollectionErrorCategoryUnsupported, 0, 0, 0,
		}},
		{"all modes", "intr 123\ncpu 100 200 300 400 500 600 700 800 900 1000\ncpu0 0 0 0 0\n", []observation.CollectionErrorCategory{
			0, 0, 0, 0,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Request nonadjacent fields out of source order.
			definitions := collect.Signals{cpusignals.GuestNiceSeconds, cpusignals.IOWaitSeconds, cpusignals.IdleSeconds, cpusignals.UserSeconds}
			wantValues := []float64{10, 5, 4, 1}
			var ids []string
			for _, definition := range definitions {
				ids = append(ids, definition.ID())
			}
			got := measure(t, fstest.MapFS{"proc/stat": {Data: []byte(tc.data)}}, collect.WithSignalID(ids...))
			for i, definition := range definitions {
				o := got[definition.ID()]
				if tc.want[i] != 0 {
					if o.GetCollectionError().GetCategory() != tc.want[i] {
						t.Errorf("%s = %v, want error %v", definition.ID(), o, tc.want[i])
					}
				} else if o.GetValue() == nil || o.GetValue().GetDoubleValue() != wantValues[i] {
					t.Errorf("%s = %v, want %v", definition.ID(), o, wantValues[i])
				}
			}
		})
	}
}

func TestActivityPermissionAndCancellation(t *testing.T) {
	fsys := fstest.MapFS{"proc/stat": {Data: []byte("cpu 100 200 300 400\n")}}
	got := measure(t, deniedFS{FS: fsys, path: "proc/stat"}, collect.WithSignalID(cpusignals.UserSeconds.ID()))
	if got[cpusignals.UserSeconds.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batch, err := New(cancelFS{FS: fsys, path: "proc/stat", cancel: cancel}).Measure(ctx, collect.WithSignalID(cpusignals.UserSeconds.ID()))
	if batch != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v, %v", batch, err)
	}
}

func TestMeasureRequestContract(t *testing.T) {
	noIO := noReadFS{t: t}
	s := New(noIO)
	var nilCtx context.Context
	for _, opts := range [][]collect.OpOption{nil, {collect.WithSignalID()}} {
		if batch, err := s.Measure(nilCtx, opts...); batch != nil || err == nil {
			t.Fatalf("nil context: %v, %v", batch, err)
		}
	}
	for _, opts := range [][]collect.OpOption{{collect.WithGPUInventory()}, {collect.WithGPUInventory(), collect.WithSignalID()}} {
		if batch, err := s.Measure(context.Background(), opts...); batch != nil || err == nil {
			t.Fatalf("unsupported inventory: %v, %v", batch, err)
		}
	}
	if batch, err := s.Measure(context.Background(), collect.WithSignalID()); err != nil || len(batch.GetObservations()) != 0 {
		t.Fatalf("empty selection: %v, %v", batch, err)
	}
}

type noReadFS struct{ t *testing.T }

func (f noReadFS) Open(path string) (fs.File, error) {
	f.t.Fatalf("unexpected source I/O: %s", path)
	return nil, fs.ErrPermission
}
