// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	hostsignals "github.com/dsx-ai-factory/health-validation/collect/signals/host"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"proc/sys/fs/file-nr": {Data: []byte("100 0 500\n")}, "proc/sys/fs/file-max": {Data: []byte("18446744073709551615\n")},
		"proc/stat":   {Data: []byte("cpu 1 2 3 4\nprocesses 18446744073709551615\nprocs_running 7\nprocs_blocked 2\n")},
		"proc/1/fd/0": {}, "proc/1/fd/1": {}, "proc/2/fd/0": {},
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
		hostsignals.FileHandlesAllocated.ID(): 100, hostsignals.FileHandlesUnused.ID(): 0,
		hostsignals.FileHandlesMax.ID(): math.MaxUint64, hostsignals.OpenFileDescriptors.ID(): 3,
		hostsignals.ProcessesCreated.ID(): math.MaxUint64, hostsignals.RunnableTasks.ID(): 7, hostsignals.BlockedTasks.ID(): 2,
	}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for id, value := range want {
		if got[id].GetCollectionError() != nil || got[id].GetValue().GetUintValue() != value {
			t.Errorf("%s = %v", id, got[id])
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

func TestIndependentMalformedFields(t *testing.T) {
	fsys := fixture()
	fsys["proc/sys/fs/file-nr"].Data = []byte("100 broken 500\n")
	fsys["proc/stat"].Data = []byte("processes 123\nprocs_running no\nprocs_blocked 2 extra\n")
	got := observations(t, fsys)
	for _, signal := range []collect.Signal{hostsignals.FileHandlesUnused, hostsignals.RunnableTasks, hostsignals.BlockedTasks} {
		if got[signal.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Errorf("%s = %v", signal.ID(), got[signal.ID()])
		}
	}
	if got[hostsignals.FileHandlesAllocated.ID()].GetValue().GetUintValue() != 100 || got[hostsignals.ProcessesCreated.ID()].GetValue().GetUintValue() != 123 {
		t.Fatal(got)
	}
}

func TestDescriptorPermissionAndDisappearance(t *testing.T) {
	got := observations(t, interceptFS{FS: fixture(), path: "proc/2/fd", err: fs.ErrPermission}, collect.WithSignalID(hostsignals.OpenFileDescriptors.ID()))
	if got[hostsignals.OpenFileDescriptors.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
		t.Fatal("partial descriptor sum reported success")
	}
	got = observations(t, interceptFS{FS: fixture(), path: "proc/2/fd", err: fs.ErrNotExist}, collect.WithSignalID(hostsignals.OpenFileDescriptors.ID()))
	if got[hostsignals.OpenFileDescriptors.ID()].GetValue().GetUintValue() != 2 {
		t.Fatal(got)
	}
}

func TestSelectionCancellationAndMissing(t *testing.T) {
	if got := observations(t, fstest.MapFS{}, collect.WithSignalID()); len(got) != 0 {
		t.Fatal(got)
	}
	got := observations(t, fstest.MapFS{"proc/sys/fs/file-max": {Data: []byte("42\n")}}, collect.WithSignalID(hostsignals.FileHandlesMax.ID()))
	if got[hostsignals.FileHandlesMax.ID()].GetValue().GetUintValue() != 42 {
		t.Fatal(got)
	}
	got = observations(t, fstest.MapFS{}, collect.WithSignalID(hostsignals.FileHandlesAllocated.ID()))
	if got[hostsignals.FileHandlesAllocated.ID()].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batch, err := New(interceptFS{FS: fixture(), path: "proc/1/fd", cancel: cancel}).Measure(ctx, collect.WithSignalID(hostsignals.OpenFileDescriptors.ID()))
	if batch != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v, %v", batch, err)
	}
}

func TestNoDescriptorTargetsAndUnknownSelection(t *testing.T) {
	got := observations(t, fstest.MapFS{"proc/sys/fs/file-max": {Data: []byte("42")}}, collect.WithSignalID(hostsignals.OpenFileDescriptors.ID()))
	if got[hostsignals.OpenFileDescriptors.ID()].GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
		t.Fatal(got)
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
