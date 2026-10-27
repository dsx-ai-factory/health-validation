// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package catalog is the NVIDIA Canonical Health Checks knowledge layer.
//
// It provides a registry of check definitions encoding NVIDIA's operational
// judgment about GPU hardware conditions, and an evaluation engine that
// produces structured health verdicts from typed measurements.
//
// Health check types (Severity, CheckDefinition, CheckVerdict, etc.) are
// defined in the [github.com/dsx-ai-factory/health-validation/catalog/health] package.
package catalog

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
)

// Catalog is a registry of health check definitions with evaluation
// capabilities. It is safe for concurrent use.
type Catalog struct {
	checks           map[string]*health.CheckDefinition
	disabledChecks   map[string]bool
	evaluationEngine *evaluation.EvaluationEngine
	overrides        OverrideSet
	architecture     string
	executionContext health.ExecutionContext
	mu               sync.RWMutex
}

// Option configures a Catalog during construction.
type Option func(*Catalog)

// EvaluationOptions configures one batch evaluation. Its zero value requests
// no cadence window.
type EvaluationOptions struct {
	// Cadence selects the run window; nil evaluates every registered check.
	Cadence *health.SchedulingCadence
}

// WithOverrides supplies operator threshold overrides.
func WithOverrides(os OverrideSet) Option {
	return func(c *Catalog) { c.overrides = os }
}

// WithDisabledChecks marks specific check IDs as disabled. Disabled
// checks appear as skipped with reason CheckDisabled.
func WithDisabledChecks(ids ...string) Option {
	return func(c *Catalog) {
		for _, id := range ids {
			c.disabledChecks[id] = true
		}
	}
}

// ErrArchitectureUnset is returned by [Catalog.Evaluate] and [Catalog.EvaluateAll]
// when the catalog was constructed without [WithArchitecture]. List, Get, and
// Register do not require it.
var ErrArchitectureUnset = errors.New("architecture must be set via WithArchitecture")

// WithArchitecture sets the current GPU architecture for filtering.
// Checks that don't apply to this architecture are skipped.
// Evaluate requires a non-empty architecture; omitting this option is valid
// for List/Overrides but fails closed at evaluation.
func WithArchitecture(arch string) Option {
	return func(c *Catalog) { c.architecture = arch }
}

// WithExecutionContext sets the deployment position. Checks that
// don't list this context are skipped.
func WithExecutionContext(ctx health.ExecutionContext) Option {
	return func(c *Catalog) { c.executionContext = ctx }
}

// NewEmpty creates a Catalog with no pre-loaded checks. Use this when
// you need full control over which checks are registered (e.g., testing
// custom check dependencies). Most users should use [New] instead.
func NewEmpty(opts ...Option) *Catalog {
	c := &Catalog{
		checks:           make(map[string]*health.CheckDefinition),
		disabledChecks:   make(map[string]bool),
		evaluationEngine: evaluation.NewEvaluationEngine(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// New creates a Catalog pre-loaded with all NVIDIA check definitions
// and configured with the given options.
func New(opts ...Option) *Catalog {
	c := &Catalog{
		checks:           make(map[string]*health.CheckDefinition),
		disabledChecks:   make(map[string]bool),
		evaluationEngine: evaluation.NewEvaluationEngine(),
	}
	// Built-ins go through the same guards as caller-supplied checks: a
	// duplicate ID or a self-contradictory threshold is an authoring error,
	// caught the first time the catalog is constructed rather than surfacing
	// as a check that silently never runs.
	for _, cd := range builtinChecks() {
		c.MustRegister(cd)
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Register adds a check definition to the catalog. Returns an error if
// the definition is invalid or if a check with the same ID is already registered.
func (c *Catalog) Register(cd health.CheckDefinition) error {
	if err := cd.Validate(); err != nil {
		return err
	}

	normalized, err := normalizeThresholds(cd.ID, cd.Thresholds)
	if err != nil {
		return err
	}
	cd.Thresholds = normalized

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.checks[cd.ID]; exists {
		return fmt.Errorf("check %q already registered", cd.ID)
	}
	stored := cloneDefinition(cd)
	c.checks[cd.ID] = &stored
	return nil
}

// MustRegister is like Register but panics on error.
func (c *Catalog) MustRegister(cd health.CheckDefinition) {
	if err := c.Register(cd); err != nil {
		panic(err)
	}
}

// normalizeThresholds returns a copy of specs with every catalog-authored
// value and bound coerced to its declared type, so CEL evaluates the type
// the check declared rather than whichever literal the author wrote.
//
// The map is rebuilt rather than mutated: Register takes the definition by
// value, but a struct copy shares its maps' backing store with the caller.
func normalizeThresholds(checkID string, specs map[string]health.ThresholdSpec) (map[string]health.ThresholdSpec, error) {
	if len(specs) == 0 {
		return specs, nil
	}

	out := make(map[string]health.ThresholdSpec, len(specs))
	for name, spec := range specs {
		normalized, err := spec.Normalize()
		if err != nil {
			return nil, &health.ThresholdValueError{ThresholdName: name, CheckID: checkID, Err: err}
		}
		out[name] = normalized
	}

	return out, nil
}

// Get returns a copy of the check definition with the given ID, or an error
// if not found. Mutating the copy does not affect the catalog.
func (c *Catalog) Get(id string) (*health.CheckDefinition, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cd, ok := c.checks[id]
	if !ok {
		return nil, fmt.Errorf("check %q not found", id)
	}
	cp := cloneDefinition(*cd)
	return &cp, nil
}

// List returns copies of all check definitions matching the given filter.
// Results are sorted by ID for deterministic output.
func (c *Catalog) List(f Filter) []*health.CheckDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*health.CheckDefinition
	for _, cd := range c.checks {
		if f.matches(cd) {
			cp := cloneDefinition(*cd)
			result = append(result, &cp)
		}
	}
	slices.SortFunc(result, func(a, b *health.CheckDefinition) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

// Categories returns the distinct categories of the registered checks,
// sorted for deterministic output — the set of domains this catalog
// expects to be able to assess, and so the set of sensors a caller must
// supply for full coverage.
func (c *Catalog) Categories() []health.Category {
	c.mu.RLock()
	defer c.mu.RUnlock()

	seen := make(map[health.Category]bool, len(c.checks))
	var cats []health.Category
	for _, cd := range c.checks {
		if !seen[cd.Category] {
			seen[cd.Category] = true
			cats = append(cats, cd.Category)
		}
	}
	slices.Sort(cats)
	return cats
}

// Len returns the number of registered check definitions.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.checks)
}

// paramsSnapshot is a point-in-time view of the host-wide and per-check
// configuration that feeds evaluation. Catalog options are intended as
// construction-time configuration, so these fields are treated as read-only
// once the catalog is in use. Under that convention, one snapshot under
// RLock is equivalent to re-reading the catalog per check and avoids O(n)
// lock round-trips on the batch path. Per-run fields such as cadence are
// set on a local copy for that evaluation; they are not stored on Catalog.
type paramsSnapshot struct {
	cadence          *health.SchedulingCadence
	disabledChecks   map[string]bool
	overrides        OverrideSet
	architecture     string
	executionContext health.ExecutionContext
}

// forCheck specializes the snapshot for a single check. Both the
// single-check Evaluate and the batch path build their Params through
// this one method so the two paths cannot drift; a divergence here is
// exactly what let the batch path silently ignore overrides and
// disabled checks.
func (s paramsSnapshot) forCheck(checkID string) evaluation.Params {
	return evaluation.Params{
		Architecture:     s.architecture,
		ExecutionContext: s.executionContext,
		Disabled:         s.disabledChecks[checkID],
		Overrides:        s.overrides.ForCheck(checkID),
		Cadence:          s.cadence,
	}
}

// snapshotParams reads the catalog's evaluation configuration under a
// single RLock. The disabledChecks map and overrides are shared by
// reference rather than deep-copied; that is sound only under the
// read-only-after-construction convention described on paramsSnapshot.
func (c *Catalog) snapshotParams() paramsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return paramsSnapshot{
		overrides:        c.overrides,
		disabledChecks:   c.disabledChecks,
		architecture:     c.architecture,
		executionContext: c.executionContext,
	}
}

func (c *Catalog) requireArchitecture() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.architecture == "" {
		return ErrArchitectureUnset
	}
	return nil
}

// Evaluate runs a single check against a typed measurement. It evaluates that
// check alone, so [health.CheckDefinition.Dependencies] has no effect here.
func (c *Catalog) Evaluate(checkID string, m health.Measurement) (health.CheckVerdict, error) {
	if err := c.requireArchitecture(); err != nil {
		return health.CheckVerdict{}, err
	}

	c.mu.RLock()
	cd, ok := c.checks[checkID]
	c.mu.RUnlock()

	if !ok {
		return health.CheckVerdict{}, fmt.Errorf("check %q not found", checkID)
	}

	return c.evaluationEngine.EvaluateCheck(cd, m, c.snapshotParams().forCheck(checkID))
}

// EvaluateAll runs all checks whose Category matches the provided
// measurements and aggregates into a node-level verdict.
//
// Each measurement is matched to checks by Category: GPU measurements
// only run GPU checks, NVLink measurements only run NVLink checks, etc.
//
// Every registered check produces a verdict, a result or a skip. A check
// whose category supplied no measurement is skipped
// [health.SensorMissing], which leaves OverallSeverity untouched but marks
// the check as unassessed in SkippedChecks. Supply the measurements for
// every category in [Catalog.Categories], or register only the checks you
// intend to feed.
//
// Each check that will run must receive at most one measurement per
// [health.EntityKey], whatever the measurements' Go types. Otherwise
// EvaluateAll returns a [health.AmbiguousMeasurementError] naming the check
// and entity, and no verdict: it does not choose between providers.
func (c *Catalog) EvaluateAll(measurements ...health.Measurement) (health.NodeVerdict, error) {
	return c.EvaluateAllWithOptions(EvaluationOptions{}, measurements...)
}

// EvaluateAllWithOptions is [Catalog.EvaluateAll] with per-run evaluation
// configuration. A zero-value EvaluationOptions evaluates every registered
// check. A requested cadence skips checks whose declared cadence does not
// cover that window.
func (c *Catalog) EvaluateAllWithOptions(opts EvaluationOptions, measurements ...health.Measurement) (health.NodeVerdict, error) {
	verdicts, err := c.evaluateAllRaw(opts, measurements...)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	return health.AggregateNodeVerdict(verdicts), nil
}

// evaluateAllRaw routes the provided measurements to the checks they can
// feed and returns the flat list of verdicts before node-level aggregation.
//
// A measurement feeds a check when they share a Category and the
// measurement's schema carries every one of the check's RequiredSignals.
// A check is evaluated once per measurement that feeds it (so a
// per-entity check yields one verdict per entity) and skipped
// NoApplicableMeasurement once when its category was collected but no
// measurement carried its required signals. An errored measurement that
// routes to a check produces a Critical verdict carrying the sensor's
// detail; it never reaches checks whose required signals its schema does
// not carry.
//
// A check whose category supplied no measurement at all is skipped
// [health.SensorMissing], with the category named in SkipDetail.
// Registering a check is what declares the expectation that its sensor
// exists, so this needs no configuration: a domain that reports nothing is
// reported as unassessed rather than dropped. The measurement-independent
// reasons are decided first, so an operator's CHECK_DISABLED still wins: a
// check turned off on purpose does not read as a sensor hole.
//
// A caller evaluating a single domain therefore receives SensorMissing
// skips for the others. That is a report, not a failure: skips do not
// affect OverallSeverity. Register only the checks you intend to feed if
// you want a narrower verdict.
func (c *Catalog) evaluateAllRaw(opts EvaluationOptions, measurements ...health.Measurement) ([]health.CheckVerdict, error) {
	if err := c.requireArchitecture(); err != nil {
		return nil, err
	}
	checks, snap := c.batchSnapshot(opts)
	return c.evaluationEngine.EvaluateBatch(checks, measurements, snap.forCheck)
}

// RunnableChecks returns the registered checks that [Catalog.EvaluateAllWithOptions]
// would not skip before reading any measurement: checks that are not
// disabled, are in the cadence window, match the architecture and execution
// context, and have their required thresholds. Use it to collect only the
// signals these checks need. A returned check can still skip during
// evaluation, for example when no measurement of its category arrives or a
// dependency was skipped.
func (c *Catalog) RunnableChecks(opts EvaluationOptions) ([]*health.CheckDefinition, error) {
	if err := c.requireArchitecture(); err != nil {
		return nil, err
	}
	checks, snap := c.batchSnapshot(opts)
	ids := evaluation.RunnableChecks(checks, snap.forCheck)
	runnable := make([]*health.CheckDefinition, 0, len(ids))
	for _, id := range ids {
		runnable = append(runnable, checks[id])
	}
	return runnable, nil
}

// batchSnapshot copies the registered checks and the evaluation context for
// one run under a single RLock.
func (c *Catalog) batchSnapshot(opts EvaluationOptions) (map[string]*health.CheckDefinition, paramsSnapshot) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	checks := make(map[string]*health.CheckDefinition, len(c.checks))
	for k, v := range c.checks {
		checks[k] = v
	}
	return checks, paramsSnapshot{
		cadence:          opts.Cadence,
		overrides:        c.overrides,
		disabledChecks:   c.disabledChecks,
		architecture:     c.architecture,
		executionContext: c.executionContext,
	}
}
