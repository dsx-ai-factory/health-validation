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

// WithArchitecture sets the current GPU architecture for filtering.
// Checks that don't apply to this architecture are skipped.
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

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.checks[cd.ID]; exists {
		return fmt.Errorf("check %q already registered", cd.ID)
	}
	c.checks[cd.ID] = &cd
	return nil
}

// MustRegister is like Register but panics on error.
func (c *Catalog) MustRegister(cd health.CheckDefinition) {
	if err := c.Register(cd); err != nil {
		panic(err)
	}
}

// Get returns the check definition with the given ID, or an error
// if not found.
func (c *Catalog) Get(id string) (*health.CheckDefinition, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cd, ok := c.checks[id]
	if !ok {
		return nil, fmt.Errorf("check %q not found", id)
	}
	return cd, nil
}

// List returns all check definitions matching the given filter.
// Results are sorted by ID for deterministic output.
func (c *Catalog) List(f Filter) []*health.CheckDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*health.CheckDefinition
	for _, cd := range c.checks {
		if f.matches(cd) {
			result = append(result, cd)
		}
	}
	slices.SortFunc(result, func(a, b *health.CheckDefinition) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return result
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
// lock round-trips on the batch path.
type paramsSnapshot struct {
	overrides        OverrideSet
	disabledChecks   map[string]bool
	architecture     string
	executionContext health.ExecutionContext
}

// forCheck specializes the snapshot for a single check. Both the
// single-check Evaluate and the batch EvaluateAllRaw build their Params
// through this one method so the two paths cannot drift; a divergence
// here is exactly what let the batch path silently ignore overrides and
// disabled checks.
func (s paramsSnapshot) forCheck(checkID string) evaluation.Params {
	return evaluation.Params{
		Architecture:     s.architecture,
		ExecutionContext: s.executionContext,
		Disabled:         s.disabledChecks[checkID],
		Overrides:        s.overrides.ForCheck(checkID),
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

// Evaluate runs a single check against a typed measurement.
func (c *Catalog) Evaluate(checkID string, m health.Measurement) (health.CheckVerdict, error) {
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
func (c *Catalog) EvaluateAll(measurements ...health.Measurement) (health.NodeVerdict, error) {
	verdicts, err := c.EvaluateAllRaw(measurements...)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	return health.AggregateNodeVerdict(verdicts), nil
}

// EvaluateAllRaw routes the provided measurements to the checks they can
// feed and returns the flat list of verdicts before node-level
// aggregation.
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
// Checks whose category is absent from measurements entirely produce no
// verdict: a partial measurement set (this is a public entry point, so
// callers may evaluate a single domain) does not raise skips for every
// other domain's checks.
func (c *Catalog) EvaluateAllRaw(measurements ...health.Measurement) ([]health.CheckVerdict, error) {
	c.mu.RLock()
	checks := make(map[string]*health.CheckDefinition, len(c.checks))
	for k, v := range c.checks {
		checks[k] = v
	}
	snap := paramsSnapshot{
		overrides:        c.overrides,
		disabledChecks:   c.disabledChecks,
		architecture:     c.architecture,
		executionContext: c.executionContext,
	}
	c.mu.RUnlock()

	return c.evaluationEngine.EvaluateBatch(checks, measurements, snap.forCheck)
}
