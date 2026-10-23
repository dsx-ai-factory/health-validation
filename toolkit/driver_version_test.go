// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"os"
	"testing"
)

func TestParseDriverVersionFile(t *testing.T) {
	contents := "NVRM version: NVIDIA UNIX x86_64 Kernel Module  575.51.03  Thu Jan  1 00:00:00 UTC 2026\nGCC version:  gcc\n"
	got, ok := parseDriverVersionFile(contents)
	if !ok {
		t.Fatal("parseDriverVersionFile failed on real /proc content")
	}
	if got.Raw != "575.51.03" {
		t.Errorf("Raw = %q, want 575.51.03", got.Raw)
	}
	// The "x86_64" segment precedes the version; a pattern without the
	// mandatory dot matches "86" here and never reaches the real one.
	if got.Major != 575 {
		t.Errorf("Major = %d, want 575 (matched an unrelated number in the line)", got.Major)
	}
}

func TestParseDriverVersionFile_NoVersionFound(t *testing.T) {
	if _, ok := parseDriverVersionFile("nothing useful here"); ok {
		t.Error("parseDriverVersionFile reported success on content with no version token")
	}
}

func TestDetectDriverVersion_MissingFile(t *testing.T) {
	orig := nvidiaVersionFile
	nvidiaVersionFile = "/nonexistent/path/for/testing"
	defer func() { nvidiaVersionFile = orig }()

	got, ok := DetectDriverVersion()
	if ok {
		t.Error("DetectDriverVersion() ok = true, want false when the version file is missing")
	}
	if got.Known() {
		t.Errorf("got %+v, want the zero value so callers cannot mistake it for a real driver", got)
	}
}

func TestDetectDriverVersion_ReadsInjectedFile(t *testing.T) {
	path := t.TempDir() + "/version"
	content := "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.10.01  Mon Feb  2 00:00:00 UTC 2026\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := nvidiaVersionFile
	nvidiaVersionFile = path
	defer func() { nvidiaVersionFile = orig }()

	got, ok := DetectDriverVersion()
	if !ok || got.Raw != "580.10.01" {
		t.Errorf("DetectDriverVersion() = (%v, %v), want (580.10.01, true)", got, ok)
	}
	if !got.AtLeast(575, 0, 0) {
		t.Errorf("%v.AtLeast(575, 0, 0) = false; the parsed parts did not survive detection", got)
	}
}
