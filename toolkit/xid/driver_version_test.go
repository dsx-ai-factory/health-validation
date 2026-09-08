package xid

import (
	"os"
	"testing"
)

func TestDriverEraFromVersion(t *testing.T) {
	tests := []struct {
		version string
		want    DriverEra
	}{
		{"575.51.03", DriverEraV2},
		{"576.02.01", DriverEraV2},
		{"574.99.99", DriverEraV1},
		{"570.86.10", DriverEraV1},
		{"", DriverEraUnknown},
		{"not-a-version", DriverEraUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := driverEraFromVersion(tt.version); got != tt.want {
				t.Errorf("driverEraFromVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

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

func TestDetectDriverEra_MissingFile(t *testing.T) {
	orig := nvidiaVersionFile
	nvidiaVersionFile = "/nonexistent/path/for/testing"
	defer func() { nvidiaVersionFile = orig }()

	if got := DetectDriverEra(); got != DriverEraUnknown {
		t.Errorf("DetectDriverEra() = %v, want DriverEraUnknown when the version file is missing", got)
	}
}

func TestDetectDriverEra_ReadsInjectedFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/version"
	content := "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.10.01  Mon Feb  2 00:00:00 UTC 2026\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := nvidiaVersionFile
	nvidiaVersionFile = path
	defer func() { nvidiaVersionFile = orig }()

	if got := DetectDriverEra(); got != DriverEraV2 {
		t.Errorf("DetectDriverEra() = %v, want DriverEraV2 for 580.x", got)
	}
}
