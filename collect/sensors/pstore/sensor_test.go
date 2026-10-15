// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pstore

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pstoresignals "github.com/dsx-ai-factory/health-validation/collect/signals/pstore"
)

type noIO struct{ calls int }

func (f *noIO) Open(string) (fs.File, error) { f.calls++; return nil, fs.ErrPermission }

func TestSelectionBeforeIO(t *testing.T) {
	files := &noIO{}
	s := New(files)
	var nilCtx context.Context
	if batch, err := s.Measure(nilCtx, collect.WithSignalID()); err == nil || batch != nil || files.calls != 0 {
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

func TestRawRecordAndCollectionTime(t *testing.T) {
	raw := "Panic#1\n[123.4] raw text\n"
	files := fstest.MapFS{"archive/123/dmesg.txt": {Data: []byte(raw), ModTime: time.Unix(1, 0)}}
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cycle, err := collect.NewCycle("pstore-cycle", started)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewWithRoots(files, "archive").Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(pstoresignals.RawMessage.ID()))
	if err != nil || len(batch.GetObservations()) != 1 || batch.CollectionId != "pstore-cycle" {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	current := batch.Observations[0]
	if current.GetValue().GetStringValue() != raw || current.Source != ID || !current.ObservedAt.AsTime().Equal(started) || current.Target.GetPstoreRecord().GetPath() != "archive/123/dmesg.txt" {
		t.Fatalf("raw record envelope = %+v", current)
	}
}

func TestAbsentRecordNoCycleRequired(t *testing.T) {
	batch, err := New(fstest.MapFS{}).Measure(context.Background(), collect.WithSignalID(pstoresignals.RawMessage.ID()))
	if err != nil || len(batch.GetObservations()) != 1 || batch.Observations[0].Target != nil || batch.Observations[0].GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
}
