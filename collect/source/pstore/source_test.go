// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pstore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pstoresignals "github.com/dsx-ai-factory/health-validation/collect/signals/pstore"
)

func TestStableIdentityAndExactRecord(t *testing.T) {
	raw := []byte("Panic#1 Part1\n[ 12.25] raw panic text\n\n")
	fixture := fstest.MapFS{"sys/fs/pstore/dmesg-ramoops-0": {Data: raw}, "var/lib/systemd/pstore/123/dmesg.txt": {Data: raw}}
	source := New(fixture)
	first, err := source.Read(context.Background(), pstoresignals.All())
	if err != nil || len(first) != 2*len(pstoresignals.All()) {
		t.Fatalf("readings = %d, %v", len(first), err)
	}
	second, err := source.Read(context.Background(), pstoresignals.All())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	for index, reading := range first {
		ref := reading.Target.GetPstoreRecord()
		if ref.GetSha256() != digest || ref.GetPath() != reading.Path || second[index].Target.GetPstoreRecord().GetSha256() != digest {
			t.Fatalf("identity = %+v", reading.Target)
		}
		if reading.Signal.ID() == pstoresignals.RawMessage.ID() && reading.Value.GetStringValue() != string(raw) {
			t.Fatalf("changed raw message: %q", reading.Value.GetStringValue())
		}
	}
	fixture["sys/fs/pstore/dmesg-ramoops-0"] = &fstest.MapFile{Data: []byte("changed")}
	changed, err := source.Read(context.Background(), collect.Signals{pstoresignals.Path})
	if err != nil || changed[0].Target.GetPstoreRecord().GetSha256() == digest {
		t.Fatalf("content change did not change identity: %+v, %v", changed, err)
	}
}

func TestBinaryRecordIsLossless(t *testing.T) {
	raw := []byte{0xff, 0x00, 0x01}
	readings, err := New(fstest.MapFS{"records/raw": {Data: raw}}, "records").Read(context.Background(), collect.Signals{pstoresignals.RawMessage, pstoresignals.ContentBase64})
	if err != nil || len(readings) != 2 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	if readings[0].Failure == nil || readings[0].Failure.Category != observation.CollectionErrorCategoryParseError {
		t.Fatalf("binary text error = %+v", readings[0])
	}
	if readings[1].Value.GetStringValue() != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("binary content changed: %+v", readings[1])
	}
}

func TestAbsentTargets(t *testing.T) {
	for _, fixture := range []fstest.MapFS{{}, {"sys/fs/pstore": {Mode: fs.ModeDir | 0o755}}} {
		readings, err := New(fixture).Read(context.Background(), collect.Signals{pstoresignals.RawMessage})
		if err != nil || len(readings) != 1 || readings[0].Target != nil || readings[0].NoValue.GetReason() != observation.NoValueReasonNoTargets {
			t.Fatalf("readings = %+v, %v", readings, err)
		}
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "records"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(root, "records", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("records", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"records", "alias"} {
		readings, err := New(os.DirFS(root), configured).Read(context.Background(), collect.Signals{pstoresignals.RawMessage})
		if err != nil || len(readings) != 1 || readings[0].Value != nil || readings[0].Failure == nil || readings[0].Failure.Category != observation.CollectionErrorCategoryUnsupported {
			t.Fatalf("symlink read = %+v, %v", readings, err)
		}
	}
}

func TestDepthLimitAndGoodSibling(t *testing.T) {
	fixture := fstest.MapFS{"records/good": {Data: []byte("raw")}, "records/a/b/c/d/hidden": {Data: []byte("not reached")}}
	readings, err := New(fixture, "records").Read(context.Background(), collect.Signals{pstoresignals.RawMessage})
	if err != nil || len(readings) != 2 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	var good, limit bool
	for _, reading := range readings {
		if reading.Path == "records/good" && reading.Value.GetStringValue() == "raw" {
			good = true
		}
		if reading.Failure != nil && reading.Path == "records/a/b/c/d" {
			limit = true
		}
	}
	if !good || !limit {
		t.Fatalf("partial scan = %+v", readings)
	}
}

type deniedRecord struct{ fs.FS }

func (d deniedRecord) Open(name string) (fs.File, error) {
	if name == "records/denied" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}

func TestPermissionFailureKeepsGoodRecord(t *testing.T) {
	fixture := fstest.MapFS{"records/denied": {Data: []byte("unreadable")}, "records/good": {Data: []byte("raw")}}
	readings, err := New(deniedRecord{fixture}, "records").Read(context.Background(), collect.Signals{pstoresignals.RawMessage})
	if err != nil || len(readings) != 2 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	if readings[0].Failure == nil || readings[0].Failure.Category != observation.CollectionErrorCategoryPermissionDenied || readings[0].Path != "records/denied" {
		t.Fatalf("permission error = %+v", readings[0])
	}
	if readings[1].Value.GetStringValue() != "raw" {
		t.Fatalf("good record lost = %+v", readings[1])
	}
}

func TestInvalidRootsDoNotEscape(t *testing.T) {
	for _, root := range []string{"../outside", "/absolute", "."} {
		readings, err := New(fstest.MapFS{}, root).Read(context.Background(), collect.Signals{pstoresignals.Path})
		if err != nil || len(readings) != 1 || readings[0].Failure == nil || readings[0].Failure.Category != observation.CollectionErrorCategoryUnsupported {
			t.Fatalf("root %q = %+v, %v", root, readings, err)
		}
	}
}
