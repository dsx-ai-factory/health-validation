// Package version reports the catalog module version compiled into a verdict
// producer.
package version

import (
	"runtime/debug"
	"strings"
	"sync"
)

const (
	// modulePath is this module's import path, matched against the entries in
	// a binary's module graph.
	modulePath = "github.com/dsx-ai-factory/health-validation/catalog"
	// development is reported when no source yields a release version. It
	// doubles as the sentinel marking the default as unstamped.
	development = "dev"
	// shortRevisionLen bounds the commit SHA appended to a development
	// version.
	shortRevisionLen = 12
)

// version is overridden by Bazel x_defs in stamped builds.
var version = development

var resolvedVersion = sync.OnceValue(func() string {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, bi)
})

// Version returns the stamped catalog version when available. Ordinary Go
// builds fall back to module build information; local and replaced builds use
// a commit-bearing development identifier when VCS metadata is available.
func Version() string {
	return resolvedVersion()
}

// resolve applies the three sources in descending order of trustworthiness:
// the link-time stamp, the module graph, then a development identifier.
func resolve(stamped string, bi *debug.BuildInfo) string {
	if v := strings.TrimSpace(stamped); v != "" && v != development {
		return v
	}
	if v := catalogModuleVersion(bi); v != "" {
		return v
	}
	return developmentVersion(bi)
}

// catalogModuleVersion reads the catalog's version from the importing binary's
// module graph.
func catalogModuleVersion(bi *debug.BuildInfo) string {
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

	revision := ""
	dirty := false
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return development
	}
	if len(revision) > shortRevisionLen {
		revision = revision[:shortRevisionLen]
	}
	v := development + "+" + revision
	if dirty {
		v += ".dirty"
	}
	return v
}
