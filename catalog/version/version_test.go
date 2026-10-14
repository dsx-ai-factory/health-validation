// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"runtime/debug"
	"testing"
)

func TestResolvePrefersStampedValue(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.0.0"}}
	if got, want := resolve(" v1.2.3 ", bi), "v1.2.3"; got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveUsesPublishedModuleVersion(t *testing.T) {
	bi := &debug.BuildInfo{Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.3"}}}
	if got, want := resolve(development, bi), "v1.2.3"; got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveDoesNotClaimReplacedModuleVersion(t *testing.T) {
	bi := &debug.BuildInfo{
		Deps: []*debug.Module{{
			Path:    modulePath,
			Version: "v1.2.3",
			Replace: &debug.Module{Path: "../catalog"},
		}},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}},
	}
	if got, want := resolve(development, bi), "dev+abcdef123456"; got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveDevelopmentFallbacks(t *testing.T) {
	t.Run("no build info", func(t *testing.T) {
		if got, want := resolve("", nil), development; got != want {
			t.Errorf("resolve() = %q, want %q", got, want)
		}
	})

	t.Run("dirty revision", func(t *testing.T) {
		bi := &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.modified", Value: "true"},
		}}
		if got, want := resolve(development, bi), "dev+abcdef123456.dirty"; got != want {
			t.Errorf("resolve() = %q, want %q", got, want)
		}
	})
}

func TestVersionIsNeverEmpty(t *testing.T) {
	if got := Version(); got == "" {
		t.Error("Version() is empty")
	}
}
