/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package version exposes build-time version metadata for the go-health-validation
// binary.
//
// The package-level vars below are overridden at link time via Bazel x_defs
// on stamped release and pre-release builds (see //build/rules/go and the
// release workspace-status script). Unstamped builds — local `go build`,
// `bazel build` without --stamp, and `bazel test` — keep the defaults, so
// `go-health-validation version` reports "dev" rather than a misleading number.
//
// As a secondary fallback, [Get] reads Go's embedded VCS build info so that
// binaries produced by `go install`/`go build` (where x_defs never runs)
// still report the commit and build time they were built from.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Build-time values, overridden via Bazel x_defs (-ldflags -X). Keep them as
// plain strings with safe defaults; see the package doc.
var (
	// version is the bare semver release tag (vX.Y.Z) on tagged builds, or a
	// strict-semver Go pseudo-version (vX.Y.Z-0.<timestamp>-<sha>) on
	// main-branch pre-release builds.
	version = "dev"
	// commit is the full git commit SHA the binary was built from.
	commit = "unknown"
	// date is the commit timestamp, RFC3339 in UTC.
	date = "unknown"
)

// Info is resolved version metadata, suitable for JSON output.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
}

// Get resolves version metadata. It prefers link-time stamped values; when
// those are absent (an unstamped build still reporting "dev"), it falls back
// to Go's embedded VCS build info so `go install`-built binaries are not
// completely opaque.
func Get() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
	}

	// Stamped build: trust the injected values verbatim.
	if version != "dev" {
		return info
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && info.Commit != "unknown" {
		info.Commit += "+dirty"
	}
	return info
}

// shortCommitLen bounds the commit SHA in the human-readable string; full
// SHAs stay available via the JSON output.
const shortCommitLen = 12

// String renders a single human-readable line, e.g.
// "go-health-validation v1.4.0 (commit a1b2c3d4e5f6, built 2026-06-02T12:00:00Z, go1.25.1)".
func (i Info) String() string {
	c := i.Commit
	// Preserve a "+dirty" marker across truncation: split it off, shorten the
	// SHA, then re-append. A naive c[:shortCommitLen] would drop it — exactly
	// the signal a reader most needs.
	suffix := ""
	if base, ok := strings.CutSuffix(c, "+dirty"); ok {
		c, suffix = base, "+dirty"
	}
	if len(c) > shortCommitLen {
		c = c[:shortCommitLen]
	}
	return fmt.Sprintf("go-health-validation %s (commit %s%s, built %s, %s)",
		i.Version, c, suffix, i.Date, i.GoVersion)
}
