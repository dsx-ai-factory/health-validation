// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveVersionPrefersStampedValue(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.0.0"}}
	if got, want := resolveVersion(" v1.2.3 ", bi), "v1.2.3"; got != want {
		t.Errorf("resolveVersion() = %q, want %q", got, want)
	}
}

func TestResolveVersionUsesPublishedModuleVersion(t *testing.T) {
	t.Run("dependency", func(t *testing.T) {
		bi := &debug.BuildInfo{Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.3"}}}
		if got, want := resolveVersion(development, bi), "v1.2.3"; got != want {
			t.Errorf("resolveVersion() = %q, want %q", got, want)
		}
	})

	t.Run("main module", func(t *testing.T) {
		bi := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}}
		if got, want := resolveVersion(development, bi), "v1.2.3"; got != want {
			t.Errorf("resolveVersion() = %q, want %q", got, want)
		}
	})
}

func TestResolveVersionDoesNotClaimReplacedModuleVersion(t *testing.T) {
	bi := &debug.BuildInfo{
		Deps: []*debug.Module{{
			Path:    modulePath,
			Version: "v1.2.3",
			Replace: &debug.Module{Path: ".."},
		}},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}},
	}
	if got, want := resolveVersion(development, bi), "dev+abcdef123456"; got != want {
		t.Errorf("resolveVersion() = %q, want %q", got, want)
	}
}

func TestResolveVersionDevelopmentFallbacks(t *testing.T) {
	t.Run("no build info", func(t *testing.T) {
		if got, want := resolveVersion("", nil), development; got != want {
			t.Errorf("resolveVersion() = %q, want %q", got, want)
		}
	})

	t.Run("dirty revision", func(t *testing.T) {
		bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.modified", Value: "true"},
		}}
		if got, want := resolveVersion(development, bi), "dev+abcdef123456.dirty"; got != want {
			t.Errorf("resolveVersion() = %q, want %q", got, want)
		}
	})
}

func TestResolveStampedTrustsInjectedMetadata(t *testing.T) {
	bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "ffffffffffff"},
		{Key: "vcs.time", Value: "2000-01-01T00:00:00Z"},
		{Key: "vcs.modified", Value: "true"},
	}}
	got := resolve("v1.4.0", "abc", "2026-06-02T12:00:00Z", bi)
	if got.Version != "v1.4.0" || got.CatalogVersion != "v1.4.0" {
		t.Errorf("versions = %q/%q, want v1.4.0 for both", got.Version, got.CatalogVersion)
	}
	if got.Commit != "abc" || got.Date != "2026-06-02T12:00:00Z" {
		t.Errorf("commit/date = %q/%q, want the injected values", got.Commit, got.Date)
	}
}

func TestResolveUnstampedBackfillsFromVCS(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.time", Value: "2026-06-02T12:00:00Z"},
		}}
		got := resolve(development, unknown, unknown, bi)
		if got.Commit != "abcdef1234567890" || got.Date != "2026-06-02T12:00:00Z" {
			t.Errorf("commit/date = %q/%q, want the VCS values", got.Commit, got.Date)
		}
		if got.Version != "dev+abcdef123456" || got.CatalogVersion != got.Version {
			t.Errorf("versions = %q/%q, want dev+abcdef123456 for both", got.Version, got.CatalogVersion)
		}
	})

	t.Run("dirty", func(t *testing.T) {
		bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.modified", Value: "true"},
		}}
		if got, want := resolve(development, unknown, unknown, bi).Commit, "abcdef1234567890+dirty"; got != want {
			t.Errorf("Commit = %q, want %q", got, want)
		}
	})

	t.Run("no VCS settings", func(t *testing.T) {
		got := resolve(development, unknown, unknown, &debug.BuildInfo{})
		if got.Commit != unknown || got.Date != unknown {
			t.Errorf("commit/date = %q/%q, want the defaults", got.Commit, got.Date)
		}
	})
}

func TestGetIsConsistent(t *testing.T) {
	got := Get()
	if got.Version == "" {
		t.Error("Version is empty")
	}
	if got.CatalogVersion != got.Version {
		t.Errorf("CatalogVersion = %q, want it to equal Version %q", got.CatalogVersion, got.Version)
	}
	if got.GoVersion == "" {
		t.Error("GoVersion is empty; want the runtime Go version")
	}
	if Version() != got.Version {
		t.Errorf("Version() = %q, want %q", Version(), got.Version)
	}
}

func TestStringContainsFields(t *testing.T) {
	info := Info{
		Version:        "v1.4.0",
		CatalogVersion: "v1.4.0",
		Commit:         "a1b2c3d4e5f6789000",
		Date:           "2026-06-02T12:00:00Z",
		GoVersion:      "go1.25.1",
	}
	s := info.String()
	// Commit is shortened to 12 chars in the human line.
	for _, want := range []string{"health-validation", "v1.4.0", "a1b2c3d4e5f6", "2026-06-02T12:00:00Z", "go1.25.1"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
	if strings.Contains(s, "789000") {
		t.Errorf("String() = %q, commit should be truncated to %d chars", s, shortCommitLen)
	}
}

func TestStringPreservesDirty(t *testing.T) {
	// A 40-char SHA plus the "+dirty" suffix is well over shortCommitLen; the
	// suffix must survive truncation (it's the whole point of the marker).
	info := Info{
		Version:        "dev",
		CatalogVersion: "dev",
		Commit:         "a1b2c3d4e5f6789000aaaabbbbccccddddeeeeff+dirty",
		Date:           "unknown",
		GoVersion:      "go1.25.1",
	}
	s := info.String()
	if !strings.Contains(s, "a1b2c3d4e5f6") {
		t.Errorf("String() = %q, want the shortened commit", s)
	}
	if !strings.Contains(s, "+dirty") {
		t.Errorf("String() = %q, want the +dirty marker preserved", s)
	}
	if strings.Contains(s, "eeeeff") {
		t.Errorf("String() = %q, commit body should be truncated", s)
	}
}

func TestInfoJSONRoundTrip(t *testing.T) {
	want := Info{Version: "v1.4.0", CatalogVersion: "v1.4.0", Commit: "abc", Date: "d", GoVersion: "go1.25.1"}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Info
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	// Spot-check the lowerCamel JSON keys consumers will depend on.
	for _, key := range []string{`"catalogVersion"`, `"goVersion"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON = %s, want a %s key", b, key)
		}
	}
}
