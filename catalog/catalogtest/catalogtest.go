// Package catalogtest provides helpers for tests that evaluate a Catalog.
package catalogtest

// DummyArchitecture is a non-empty architecture identifier for tests that
// must call Evaluate but do not exercise architecture filtering or
// per-architecture thresholds.
const DummyArchitecture = "dummyArch"
