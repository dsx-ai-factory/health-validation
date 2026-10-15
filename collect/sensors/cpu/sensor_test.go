// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu

import (
	"context"
	"errors"
	"io/fs"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
)

func healthyFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/devices/system/cpu/possible": {Data: []byte("0-143\n")},
		"sys/devices/system/cpu/online":   {Data: []byte("0-141\n")},
		"sys/devices/system/node/online":  {Data: []byte("0-1\n")},
	}
}

// deniedFS fails every read of one path with a permission error.
type deniedFS struct {
	fs.FS
	path string
}

func (d deniedFS) Open(name string) (fs.File, error) {
	if name == d.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}

func measure(t *testing.T, fsys fs.FS, opts ...collect.OpOption) map[string]*observation.Observation {
	t.Helper()
	// Existing tests exercise topology; activity has separate fixture coverage.
	if len(opts) == 0 {
		opts = []collect.OpOption{collect.WithSignalID(cpusignals.PossibleCount.ID(), cpusignals.OnlineCount.ID(), cpusignals.NUMANodeOnlineCount.ID())}
	}
	cycle, err := collect.NewCycle("cycle-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := New(fsys).Measure(context.Background(), append(opts, collect.WithCycle(cycle))...)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if batch.GetCollectionId() != "cycle-1" {
		t.Errorf("collection ID = %q, want cycle-1", batch.GetCollectionId())
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
	out := make(map[string]*observation.Observation)
	for _, o := range batch.GetObservations() {
		if o.GetSource() != ID || o.GetTarget().GetNode() == nil {
			t.Errorf("observation %q: source %q target %v, want %q node target", o.GetSignalId(), o.GetSource(), o.GetTarget(), ID)
		}
		out[o.GetSignalId()] = o
	}
	return out
}

func TestMeasureHealthy(t *testing.T) {
	got := measure(t, healthyFS())
	want := map[string]int64{
		cpusignals.PossibleCount.ID():       144,
		cpusignals.OnlineCount.ID():         142,
		cpusignals.NUMANodeOnlineCount.ID(): 2,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d observations, want %d", len(got), len(want))
	}
	for id, value := range want {
		if v := got[id].GetValue().GetIntValue(); v != value {
			t.Errorf("%s = %d, want %d", id, v, value)
		}
	}
}

// Each broken file fails only its own signal, with a category that says why.
func TestMeasurePerSignalFailures(t *testing.T) {
	missing := healthyFS()
	delete(missing, "sys/devices/system/node/online")
	malformed := healthyFS()
	malformed["sys/devices/system/cpu/online"] = &fstest.MapFile{Data: []byte("0-3,2")}

	tests := []struct {
		fsys     fs.FS
		name     string
		signal   string
		category observation.CollectionErrorCategory
	}{
		{name: "missing", fsys: missing, signal: cpusignals.NUMANodeOnlineCount.ID(), category: observation.CollectionErrorCategoryUnavailable},
		{name: "malformed", fsys: malformed, signal: cpusignals.OnlineCount.ID(), category: observation.CollectionErrorCategoryParseError},
		{
			name: "permission", fsys: deniedFS{FS: healthyFS(), path: "sys/devices/system/cpu/possible"},
			signal: cpusignals.PossibleCount.ID(), category: observation.CollectionErrorCategoryPermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for id, o := range measure(t, tt.fsys) {
				failure := o.GetCollectionError()
				if id != tt.signal {
					if failure != nil {
						t.Errorf("%s failed: %v", id, failure)
					}
					continue
				}
				if failure.GetCategory() != tt.category || failure.GetSource() != ID {
					t.Errorf("%s failure = %v, want category %v from %s", id, failure, tt.category, ID)
				}
			}
		})
	}
}

func TestMeasureIndependentFailuresAccumulate(t *testing.T) {
	fsys := deniedFS{FS: fstest.MapFS{
		"sys/devices/system/cpu/possible": {Data: []byte("one")},
		"sys/devices/system/cpu/online":   {Data: []byte("0-3")},
	}, path: "sys/devices/system/cpu/online"}
	want := map[string]observation.CollectionErrorCategory{
		cpusignals.PossibleCount.ID():       observation.CollectionErrorCategoryParseError,
		cpusignals.OnlineCount.ID():         observation.CollectionErrorCategoryPermissionDenied,
		cpusignals.NUMANodeOnlineCount.ID(): observation.CollectionErrorCategoryUnavailable,
	}
	got := measure(t, fsys)
	if len(got) != len(want) {
		t.Fatalf("got %d observations, want %d", len(got), len(want))
	}
	for id, category := range want {
		if c := got[id].GetCollectionError().GetCategory(); c != category {
			t.Errorf("%s category = %v, want %v", id, c, category)
		}
	}
}

// cancelFS cancels its context when path is opened.
type cancelFS struct {
	fs.FS
	cancel context.CancelFunc
	path   string
}

func (c cancelFS) Open(name string) (fs.File, error) {
	if name == c.path {
		c.cancel()
	}
	return c.FS.Open(name)
}

// Cancellation during any read fails the whole call, even after earlier
// reads succeeded.
func TestMeasureCanceledDuringRead(t *testing.T) {
	for _, path := range []string{
		"sys/devices/system/cpu/possible",
		"sys/devices/system/cpu/online",
		"sys/devices/system/node/online",
	} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			batch, err := New(cancelFS{FS: healthyFS(), cancel: cancel, path: path}).Measure(ctx)
			if !errors.Is(err, context.Canceled) || batch != nil {
				t.Fatalf("Measure = %v, %v; want nil, context.Canceled", batch, err)
			}
		})
	}
}

func TestMeasureSelectsSignals(t *testing.T) {
	got := measure(t, healthyFS(), collect.WithSignalID(cpusignals.OnlineCount.ID()))
	if len(got) != 1 || got[cpusignals.OnlineCount.ID()] == nil {
		t.Fatalf("got %v, want only %s", got, cpusignals.OnlineCount.ID())
	}
}

func TestMeasureCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	batch, err := New(healthyFS()).Measure(ctx)
	if !errors.Is(err, context.Canceled) || batch != nil {
		t.Fatalf("Measure = %v, %v; want nil, context.Canceled", batch, err)
	}
}

type blockedCPUFS struct {
	fs.FS
	started chan struct{}
	release chan struct{}
	path    string
	calls   atomic.Int32
}

func (f *blockedCPUFS) Open(name string) (fs.File, error) {
	if name == f.path {
		if f.calls.Add(1) == 1 {
			close(f.started)
		}
		<-f.release
	}
	return f.FS.Open(name)
}

func TestMeasureTimeoutCoversTopologyAndActivity(t *testing.T) {
	for _, path := range []string{"sys/devices/system/cpu/online", "proc/stat", "proc/loadavg"} {
		t.Run(path, func(t *testing.T) {
			fsys := healthyFS()
			fsys["proc/stat"] = &fstest.MapFile{Data: []byte("cpu 100 200 300 400\n")}
			fsys["proc/loadavg"] = &fstest.MapFile{Data: []byte("1 2 3\n")}
			blocked := &blockedCPUFS{FS: fsys, path: path, started: make(chan struct{}), release: make(chan struct{})}
			defer close(blocked.release)
			startedAt := time.Now()
			cycle, err := collect.NewCycleWithTimeout("cpu-timeout", startedAt, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			s := New(blocked)
			ids := []string{cpusignals.OnlineCount.ID(), cpusignals.UserSeconds.ID(), cpusignals.Load5.ID()}
			opts := []collect.OpOption{collect.WithCycle(cycle), collect.WithSignalID(ids...)}
			done := make(chan struct{})
			var batch *observation.ObservationBatch
			var measureErr error
			go func() {
				batch, measureErr = s.Measure(context.Background(), opts...)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Measure did not stop waiting for blocked acquisition")
			}
			select {
			case <-blocked.started:
			default:
				t.Fatal("acquisition did not reach blocked source")
			}
			if measureErr != nil || len(batch.GetObservations()) != len(ids) {
				t.Fatalf("Measure = %v, %v", batch, measureErr)
			}
			if batch.GetCollectionId() != cycle.CollectionID() {
				t.Fatalf("collection ID = %q", batch.GetCollectionId())
			}
			for i, o := range batch.Observations {
				if o.SignalId != ids[i] || o.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout || !o.ObservedAt.AsTime().Equal(startedAt) {
					t.Errorf("timeout observation = %v", o)
				}
			}
			if validateErr := observation.ValidateBatch(batch); validateErr != nil {
				t.Fatal(validateErr)
			}
			repeated, err := s.Measure(context.Background(), opts...)
			if err != nil || len(repeated.GetObservations()) != len(ids) || blocked.calls.Load() != 1 {
				t.Fatalf("same-cycle retry = %v, %v; blocked opens = %d", repeated, err, blocked.calls.Load())
			}
			for _, o := range repeated.Observations {
				if o.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
					t.Errorf("same-cycle retry = %v", o)
				}
			}
		})
	}
}
