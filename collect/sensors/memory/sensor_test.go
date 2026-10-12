// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	memorysignals "github.com/dsx-ai-factory/health-validation/collect/signals/memory"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"proc/meminfo":     {Data: []byte("MemTotal: 100 kB\nMemFree: 10 kB\nMemAvailable: 30 kB\nVmallocTotal: 18014398509481983 kB\nVmallocUsed: 2 kB\n")},
		"proc/vmallocinfo": {Data: []byte("0-1 4096 bpf_jit_alloc_exec+0x1\n0-2 8192 bpf_jit_alloc_exec\n0-3 100 other\n")},
	}
}

func observations(t *testing.T, fsys fs.FS, opts ...collect.OpOption) map[string]*observation.Observation {
	t.Helper()
	batch, err := New(fsys).Measure(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*observation.Observation)
	for _, o := range batch.Observations {
		out[o.SignalId] = o
	}
	return out
}

func TestAccounting(t *testing.T) {
	got := observations(t, fixture())
	want := map[string]uint64{
		memorysignals.TotalBytes.ID(): 102400, memorysignals.FreeBytes.ID(): 10240,
		memorysignals.AvailableBytes.ID(): 30720, memorysignals.UsedBytes.ID(): 71680,
		memorysignals.VmallocTotalBytes.ID(): math.MaxUint64 - 1023, memorysignals.VmallocUsedBytes.ID(): 2048, memorysignals.BPFJITBytes.ID(): 12288,
	}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for id, value := range want {
		if got[id].GetCollectionError() != nil || got[id].GetValue().GetUintValue() != value {
			t.Errorf("%s = %v, want %d", id, got[id], value)
		}
	}
}

type interceptFS struct {
	fs.FS
	err    error
	cancel context.CancelFunc
	path   string
}

func (f interceptFS) Open(path string) (fs.File, error) {
	if path == f.path {
		if f.cancel != nil {
			f.cancel()
		}
		if f.err != nil {
			return nil, f.err
		}
	}
	return f.FS.Open(path)
}

func TestIndependentFailures(t *testing.T) {
	fsys := fixture()
	fsys["proc/meminfo"].Data = []byte("MemTotal: 10 kB\nMemFree: nope kB\nMemAvailable: 20 kB\nVmallocTotal: 18446744073709551615 kB\nVmallocUsed: 1 MB\n")
	got := observations(t, interceptFS{FS: fsys, path: "proc/vmallocinfo", err: fs.ErrPermission})
	if got[memorysignals.TotalBytes.ID()].GetValue().GetUintValue() != 10240 {
		t.Fatal(got)
	}
	for _, signal := range []collect.Signal{memorysignals.FreeBytes, memorysignals.UsedBytes, memorysignals.VmallocTotalBytes, memorysignals.VmallocUsedBytes} {
		if got[signal.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Errorf("%s = %v", signal.ID(), got[signal.ID()])
		}
	}
	if got[memorysignals.BPFJITBytes.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
		t.Fatal("denied vmalloc became zero success")
	}
}

func TestSelectionAndCancellation(t *testing.T) {
	if got := observations(t, fstest.MapFS{}, collect.WithSignalID()); len(got) != 0 {
		t.Fatal(got)
	}
	got := observations(t, fstest.MapFS{"proc/meminfo": {Data: []byte("MemFree: 3 kB\n")}}, collect.WithSignalID(memorysignals.FreeBytes.ID()))
	if got[memorysignals.FreeBytes.ID()].GetValue().GetUintValue() != 3072 {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batch, err := New(interceptFS{FS: fixture(), path: "proc/meminfo", cancel: cancel}).Measure(ctx)
	if batch != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v, %v", batch, err)
	}
}

func TestBPFParseAndMissingMemory(t *testing.T) {
	for _, text := range []string{"0-1 broken bpf_jit_alloc_exec", "0-1 18446744073709551615 bpf_jit_alloc_exec\n0-2 1 bpf_jit_alloc_exec"} {
		got := observations(t, fstest.MapFS{"proc/vmallocinfo": {Data: []byte(text)}}, collect.WithSignalID(memorysignals.BPFJITBytes.ID()))
		if got[memorysignals.BPFJITBytes.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Fatal(got)
		}
	}
	got := observations(t, fstest.MapFS{"proc/meminfo": {Data: []byte("MemFree: 1 kB\n")}}, collect.WithSignalID(memorysignals.AvailableBytes.ID()))
	if got[memorysignals.AvailableBytes.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnsupported {
		t.Fatal(got)
	}
	got = observations(t, fstest.MapFS{}, collect.WithSignalID(memorysignals.TotalBytes.ID()))
	if got[memorysignals.TotalBytes.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatal(got)
	}
}

func TestMalformedMemoryAndUnknownSelection(t *testing.T) {
	for _, text := range []string{"not meminfo", "MemTotal: 1 kB\nMemTotal: 2 kB"} {
		got := observations(t, fstest.MapFS{"proc/meminfo": {Data: []byte(text)}}, collect.WithSignalID(memorysignals.TotalBytes.ID()))
		if got[memorysignals.TotalBytes.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Fatal(got)
		}
	}
	batch, err := New(fixture()).Measure(context.Background(), collect.WithSignalID("unknown"))
	if batch != nil || err == nil {
		t.Fatalf("%v, %v", batch, err)
	}
}

func TestMeasureRequestContract(t *testing.T) {
	noIO := noReadFS{t: t}
	s := New(noIO)
	for _, opts := range [][]collect.OpOption{nil, {collect.WithSignalID()}} {
		if batch, err := s.Measure(nil, opts...); batch != nil || err == nil {
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
