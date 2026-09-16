// Package catalog is the registry of NVIDIA Canonical Health Checks and the
// entry point for evaluating measurements against them.
//
// A [Catalog] holds [health.CheckDefinition]s — the operational judgment
// NVIDIA encodes about node health — and exposes [Catalog.EvaluateAll]
// to turn typed measurements into a [health.NodeVerdict] consumable by
// automation. Built-in NVIDIA checks are loaded automatically by [New];
// callers can register additional checks via [Catalog.Register].
//
// # Basic use
//
// Construct a catalog with the host's architecture and execution context,
// then evaluate measurements supplied by a sensor:
//
//	cat := catalog.New(
//	    catalog.WithArchitecture("GB200"), // any architecture identifier the catalog knows about
//	    catalog.WithExecutionContext(health.OperatorInfra),
//	)
//
//	m := gpu.GPUMeasurement{GPUID: "GPU-0"}
//	nv, err := cat.EvaluateAll(nil, m)
//	if err != nil { /* handle */ }
//	os.Exit(nv.ExitCode())
//
// Each measurement is matched to checks by [health.Category]; one
// CheckVerdict is produced per (measurement, matching check) pair, and
// [health.AggregateNodeVerdict] rolls them into the node verdict.
//
// [Catalog.EvaluateAll] takes an optional cadence. nil considers every
// registered check. A non-nil window keeps only checks whose declared
// cadence covers it; the rest are omitted (no verdict, not a skip). Cadence
// is an argument on each call, not a catalog option.
//
// For checks that remain, registration means a sensor is expected: each
// yields a result or a skip. A category with no measurement is skipped
// [health.SensorMissing]. Pass measurements for every category in
// [Catalog.Categories], or register only the checks you intend to feed.
//
// # Architectures
//
// Each [health.CheckDefinition] declares the architectures it applies
// to (e.g. "GB200", "H100", "H200", "A100"). Evaluate requires
// [WithArchitecture]; an unset architecture is an error, not a
// filter-off. At evaluation time the catalog skips checks whose
// architecture list does not include the configured one, so
// architecture-specific thresholds and rules coexist in the same
// registry. New architectures slot in by adding their identifier to
// an existing check's list or by registering a new architecture-specific
// check.
//
// # Operator overrides
//
// Adjustable thresholds (e.g. expected GPU count per fleet) accept
// operator overrides via [WithOverrides] and an [OverrideSet] built from
// [Override] entries. Fixed thresholds reject overrides at construction
// time with a [health.FixedThresholdError].
//
// # Domain check packages
//
// Per-domain check definitions (CPU, node, NVLink, XID, …) live in
// subpackages such as [catalog/cpu], [catalog/node], [catalog/nvlink],
// and [catalog/xid]. The catalog's [New] constructor already registers
// them; consumers seldom need to import the domain packages directly.
package catalog
