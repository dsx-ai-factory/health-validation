package process

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	processsignals "github.com/dsx-ai-factory/health-validation/collect/signals/process"
)

const fixtureBootID = "01234567-89ab-cdef-0123-456789abcdef"

func fixtureStat(start uint64) []byte {
	return []byte(fmt.Sprintf("42 (nvidia-blocker) D %s%d 0\n", strings.Repeat("0 ", 18), start))
}

func fixtureFS() fstest.MapFS {
	return fstest.MapFS{"proc/sys/kernel/random/boot_id": &fstest.MapFile{Data: []byte(fixtureBootID)}, "proc/42/stat": &fstest.MapFile{Data: fixtureStat(500)}, "proc/42/comm": &fstest.MapFile{Data: []byte("nvidia-blocker\n")}}
}

func TestSnapshotAndPIDReuse(t *testing.T) {
	fsys := fixtureFS()
	s := New(fsys)
	cycle, _ := collect.NewCycle("process-cycle", time.Unix(100, 0))
	batch, err := s.Measure(context.Background(), collect.WithCycle(cycle))
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	if batch.CollectionId != "process-cycle" {
		t.Fatal("cycle identity lost")
	}
	for _, current := range batch.Observations {
		if !current.ObservedAt.AsTime().Equal(cycle.StartedAt()) {
			t.Fatal("cycle timestamp lost")
		}
		if current.SignalId == processsignals.Name.ID() && current.GetValue().GetStringValue() != "nvidia-blocker" {
			t.Fatal("comm changed")
		}
		if current.SignalId == processsignals.BlockedCount.ID() && current.GetValue().GetUintValue() != 1 {
			t.Fatal("blocked count lost")
		}
	}
	fsys["proc/42/stat"].Data = fixtureStat(600)
	second, err := s.Measure(context.Background(), collect.WithSignalID(processsignals.State.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if second.Observations[0].GetTarget().GetProcess().GetStartTimeTicks() != 600 {
		t.Fatal("PID reuse retained old identity")
	}
}

type failingFS struct {
	err   error
	files fstest.MapFS
	path  string
}

func (f failingFS) Open(path string) (fs.File, error) {
	if path == f.path {
		return nil, &fs.PathError{Op: "open", Path: path, Err: f.err}
	}
	return f.files.Open(path)
}

func TestMissingDeniedMalformedAndEmptySelection(t *testing.T) {
	for _, test := range []struct {
		err      error
		name     string
		category observation.CollectionErrorCategory
	}{
		{name: "disappeared", err: fs.ErrNotExist, category: observation.CollectionErrorCategoryUnspecified},
		{name: "denied", err: fs.ErrPermission, category: observation.CollectionErrorCategoryPermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := New(failingFS{files: fixtureFS(), path: "proc/42/stat", err: test.err})
			batch, err := s.Measure(context.Background(), collect.WithSignalID(processsignals.State.ID(), processsignals.Count.ID()))
			if err != nil {
				t.Fatal(err)
			}
			if err := observation.ValidateBatch(batch); err != nil {
				t.Fatal(err)
			}
			for _, current := range batch.Observations {
				if test.name == "disappeared" {
					if current.SignalId == processsignals.State.ID() && current.GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
						t.Fatal("disappearing PID not no-target")
					}
				} else if current.GetCollectionError().GetCategory() != test.category {
					t.Fatal("permission error suppressed")
				}
			}
		})
	}
	fsys := fixtureFS()
	fsys["proc/42/stat"].Data = []byte("malformed")
	batch, err := New(fsys).Measure(context.Background(), collect.WithSignalID(processsignals.Count.ID()))
	if err != nil || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryParseError {
		t.Fatalf("parse failure: %v %+v", err, batch)
	}
	batch, err = New(failingFS{path: "proc", err: fs.ErrPermission}).Measure(context.Background(), collect.WithSignalID())
	if err != nil || len(batch.Observations) != 0 {
		t.Fatal("empty selection performed acquisition")
	}
}

type blockingProcessFS struct {
	fs.FS
	release chan struct{}
}

func (f blockingProcessFS) Open(path string) (fs.File, error) {
	<-f.release
	return f.FS.Open(path)
}

func TestCycleTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cycle, err := collect.NewCycleWithTimeout("process-timeout", time.Now(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := New(blockingProcessFS{FS: fixtureFS(), release: release}).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(processsignals.Count.ID()))
	if err != nil || len(batch.GetObservations()) != 1 || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("got %v, %v; want timeout observation", batch, err)
	}
}
