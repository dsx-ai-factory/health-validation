package xid

import (
	"os"
	"testing"
)

func TestParseDriverVersion(t *testing.T) {
	contents := "NVRM version: NVIDIA UNIX x86_64 Kernel Module  575.51.03  Thu Jan  1 00:00:00 UTC 2026\nGCC version:  gcc\n"
	got, err := parseDriverVersion(contents)
	if err != nil {
		t.Fatalf("parseDriverVersion: %v", err)
	}
	if got != "575.51.03" {
		t.Errorf("parseDriverVersion = %q, want 575.51.03", got)
	}
}

func TestParseDriverVersion_NoVersionFound(t *testing.T) {
	if _, err := parseDriverVersion("nothing useful here"); err == nil {
		t.Error("expected an error for content with no version token")
	}
}

func TestDetectDriverVersion_MissingFile(t *testing.T) {
	orig := nvidiaVersionFile
	nvidiaVersionFile = "/nonexistent/path/for/testing"
	defer func() { nvidiaVersionFile = orig }()

	if _, ok := DetectDriverVersion(); ok {
		t.Error("DetectDriverVersion() ok = true, want false when the version file is missing")
	}
}

func TestDetectDriverVersion_ReadsInjectedFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/version"
	content := "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.10.01  Mon Feb  2 00:00:00 UTC 2026\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := nvidiaVersionFile
	nvidiaVersionFile = path
	defer func() { nvidiaVersionFile = orig }()

	got, ok := DetectDriverVersion()
	if !ok || got != "580.10.01" {
		t.Errorf("DetectDriverVersion() = (%q, %v), want (580.10.01, true)", got, ok)
	}
}
