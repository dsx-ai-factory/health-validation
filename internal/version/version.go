// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package version reports the version and build metadata of the
// health-validation module compiled into a binary.
//
// The package-level vars below are overridden at link time via Bazel x_defs
// on stamped release and pre-release builds (see //build/rules/go and the
// release workspace-status script). Without a stamp, the version comes from
// the binary's module build information, then falls back to a development
// identifier carrying the VCS revision when one is embedded. Commit and date
// fall back to the same VCS settings, so binaries produced by
// `go install`/`go build` still report where they were built from.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

const (
	// modulePath is this module's import path, matched against the entries in
	// a binary's module graph.
	modulePath = "github.com/dsx-ai-factory/health-validation"
	// development is reported when no source yields a release version. It
	// doubles as the sentinel marking the default as unstamped.
	development = "dev"
	unknown     = "unknown"
	// shortRevisionLen bounds the commit SHA appended to a development
	// version.
	shortRevisionLen = 12
	// shortCommitLen bounds the commit SHA in the human-readable string; full
	// SHAs stay available via the JSON output.
	shortCommitLen = 12
)

// Build-time values, overridden via Bazel x_defs (-ldflags -X). Keep them as
// plain strings with safe defaults; see the package doc.
var (
	// version is the bare semver release tag (vX.Y.Z) on tagged builds, or a
	// strict-semver Go pseudo-version (vX.Y.Z-0.<timestamp>-<sha>) on
	// main-branch pre-release builds.
	version = development
	// commit is the full git commit SHA the binary was built from.
	commit = unknown
	// date is the commit timestamp, RFC3339 in UTC.
	date = unknown
)

// Info is resolved version metadata, suitable for JSON output.
type Info struct {
	Version string `json:"version"`
	// CatalogVersion is the version of the catalog compiled into the binary.
	// The catalog is part of this module, so it always equals Version.
	CatalogVersion string `json:"catalogVersion"`
	Commit         string `json:"commit"`
	Date           string `json:"date"`
	GoVersion      string `json:"goVersion"`
}

var current = sync.OnceValue(func() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, commit, date, bi)
})

// Get returns the resolved version metadata.
func Get() Info {
	return current()
}

// Version returns the resolved module version.
func Version() string {
	return Get().Version
}

// resolve applies the sources for each field in descending order of
// trustworthiness: the link-time stamp, then Go's embedded build information.
// A stamped build trusts the injected commit and date verbatim.
func resolve(stampedVersion, stampedCommit, stampedDate string, bi *debug.BuildInfo) Info {
	v := resolveVersion(stampedVersion, bi)
	info := Info{
		Version:        v,
		CatalogVersion: v,
		Commit:         stampedCommit,
		Date:           stampedDate,
		GoVersion:      runtime.Version(),
	}
	if isStamped(stampedVersion) || bi == nil {
		return info
	}

	vcs := vcsSettings(bi)
	if vcs.revision != "" {
		info.Commit = vcs.revision
	}
	if vcs.time != "" {
		info.Date = vcs.time
	}
	if vcs.modified && info.Commit != unknown {
		info.Commit += "+dirty"
	}
	return info
}

func isStamped(stamped string) bool {
	v := strings.TrimSpace(stamped)
	return v != "" && v != development
}

// resolveVersion applies the three sources in descending order of
// trustworthiness: the link-time stamp, the module graph, then a development
// identifier.
func resolveVersion(stamped string, bi *debug.BuildInfo) string {
	if isStamped(stamped) {
		return strings.TrimSpace(stamped)
	}
	if v := moduleVersion(bi); v != "" {
		return v
	}
	return developmentVersion(bi)
}

// moduleVersion reads this module's version from the binary's module graph.
func moduleVersion(bi *debug.BuildInfo) string {
	if bi == nil {
		return ""
	}
	if bi.Main.Path == modulePath {
		if v := effectiveModuleVersion(&bi.Main); v != "" {
			return v
		}
	}
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			return effectiveModuleVersion(dep)
		}
	}
	return ""
}

func effectiveModuleVersion(module *debug.Module) string {
	if module.Replace != nil {
		module = module.Replace
	}
	v := strings.TrimSpace(module.Version)
	if v == "" || v == "(devel)" {
		return ""
	}
	return v
}

func developmentVersion(bi *debug.BuildInfo) string {
	if bi == nil {
		return development
	}

	vcs := vcsSettings(bi)
	if vcs.revision == "" {
		return development
	}
	revision := vcs.revision
	if len(revision) > shortRevisionLen {
		revision = revision[:shortRevisionLen]
	}
	v := development + "+" + revision
	if vcs.modified {
		v += ".dirty"
	}
	return v
}

type vcs struct {
	revision string
	time     string
	modified bool
}

func vcsSettings(bi *debug.BuildInfo) vcs {
	var v vcs
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			v.revision = s.Value
		case "vcs.time":
			v.time = s.Value
		case "vcs.modified":
			v.modified = s.Value == "true"
		}
	}
	return v
}

// String renders a single human-readable line, e.g.
// "health-validation v1.4.0 (commit a1b2c3d4e5f6, built 2026-06-02T12:00:00Z, go1.25.1)".
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
	return fmt.Sprintf("health-validation %s (commit %s%s, built %s, %s)",
		i.Version, c, suffix, i.Date, i.GoVersion)
}
