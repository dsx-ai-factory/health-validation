// Package evaluation implements the health check evaluation pipeline.
//
// It evaluates check definitions against signal values using CEL
// expressions, resolves threshold overrides, and aggregates per-check
// verdicts into a node-level health assessment.
package evaluation

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/internal/celeval"
)

// EvaluationEngine evaluates health check definitions against signal values.
// It is safe for concurrent use.
type EvaluationEngine struct {
	cel *celeval.Evaluator
}

// NewEvaluationEngine creates an evaluation engine.
func NewEvaluationEngine() *EvaluationEngine {
	return &EvaluationEngine{cel: celeval.New()}
}

// Params holds the evaluation context that is independent of any
// particular measurement: how the host is configured, which thresholds
// the operator has overridden, whether checks have been disabled.
// Per-measurement state (signal values, the errored-entity placeholder)
// is supplied via the Measurement passed to EvaluateCheck and
// EvaluateAllRaw, not through this struct.
type Params struct {
	Overrides        map[string]any
	Architecture     string
	ExecutionContext health.ExecutionContext
	Disabled         bool
}

// EvaluateCheck runs a single check against one measurement.
//
// The measurement-independent skips apply first (disabled,
// architecture, execution context, unconfigured required threshold).
// If the check survives those, the measurement must route to it — same
// Category and a schema carrying every RequiredSignal (see [routes]) —
// or the check skips with [health.NoApplicableMeasurement]. A routed
// measurement then runs through error attribution and CEL: an error
// covering a required signal short-circuits to Critical, otherwise the
// rules evaluate against the populated fields.
func (e *EvaluationEngine) EvaluateCheck(cd *health.CheckDefinition, m health.Measurement, p Params) (health.CheckVerdict, error) {
	now := time.Now()
	if v, isSkip := checkLevelSkip(cd, p, now); isSkip {
		return v, nil
	}
	signals := ToSignals(m)
	if !routes(cd, m.Category(), signals) {
		return skippedVerdict(cd, now, health.NoApplicableMeasurement, noApplicableDetail(cd)), nil
	}
	return e.evaluateAgainst(cd, signals, m.Errors(), p, now)
}

// EvaluateBatch routes every measurement to the checks it can feed and
// returns the flat verdict list before node-level aggregation.
//
// A measurement feeds a check when they share a Category and the
// measurement's schema carries all of the check's RequiredSignals (see
// [routes]). Each check is evaluated once per measurement that feeds it
// — so a per-entity check yields one verdict per entity — and exactly
// once as a [health.NoApplicableMeasurement] skip when its category was
// collected but no measurement carried its required signals. The
// measurement-independent skips (disabled, architecture, context,
// unconfigured threshold, dependency) are decided once per check, not
// once per measurement.
//
// A check whose Category is absent from measurements entirely produces
// no verdict at all: the domain simply was not part of this evaluation,
// so it is neither a result nor a skip. This keeps a partial measurement
// set (these methods are a public entry point; callers may evaluate one
// domain at a time) from emitting NoApplicableMeasurement skips for every
// other domain's checks. NoApplicableMeasurement is reserved for the
// sharper case: the domain was collected but this check's measurement
// shape was not among what the sensor produced.
//
// paramsFor supplies the per-check evaluation context; the engine asks
// for fresh Params per check so overrides and disabled state stay
// per-check, matching the single-check EvaluateCheck path.
func (e *EvaluationEngine) EvaluateBatch(checks map[string]*health.CheckDefinition, measurements []health.Measurement, paramsFor func(checkID string) Params) ([]health.CheckVerdict, error) {
	now := time.Now()

	// Extract each measurement's signals once; every check reuses them.
	type observed struct {
		signals  map[string]any
		errs     []health.ErroredMeasurement
		category health.Category
	}
	obs := make([]observed, len(measurements))
	presentCategories := make(map[health.Category]bool)
	for i, m := range measurements {
		cat := m.Category()
		obs[i] = observed{ToSignals(m), m.Errors(), cat}
		presentCategories[cat] = true
	}

	order := TopologicalOrder(checks)
	verdicts := make([]health.CheckVerdict, 0, len(order))
	skipped := make(map[string]bool, len(order))

	for _, id := range order {
		cd := checks[id]

		// A check whose whole domain was not collected is out of scope for
		// this batch: emit nothing rather than a NoApplicableMeasurement skip.
		if !presentCategories[cd.Category] {
			continue
		}

		if dep, ok := skippedDependency(cd, skipped); ok {
			verdicts = append(verdicts, skippedVerdict(cd, now, health.DependencySkipped,
				fmt.Sprintf("dependency %q was skipped", dep)))
			skipped[id] = true
			continue
		}

		p := paramsFor(id)
		if v, isSkip := checkLevelSkip(cd, p, now); isSkip {
			verdicts = append(verdicts, v)
			skipped[id] = true
			continue
		}

		matched := false
		for _, o := range obs {
			if !routes(cd, o.category, o.signals) {
				continue
			}
			v, err := e.evaluateAgainst(cd, o.signals, o.errs, p, now)
			if err != nil {
				return nil, fmt.Errorf("evaluating %q: %w", id, err)
			}
			verdicts = append(verdicts, v)
			matched = true
		}
		if !matched {
			verdicts = append(verdicts, skippedVerdict(cd, now, health.NoApplicableMeasurement,
				noApplicableDetail(cd)))
			skipped[id] = true
		}
	}

	return verdicts, nil
}

// routes reports whether a measurement of the given category and signal
// schema can feed the check: the categories must match and the schema
// must carry every RequiredSignal. Signal-name uniqueness within a
// category — enforced by [catalog.CheckSignalUniqueness] — is what makes
// this a precise measurement-to-check routing rather than a coarse
// category match.
func routes(cd *health.CheckDefinition, category health.Category, signals map[string]any) bool {
	if cd.Category != category {
		return false
	}
	for _, sig := range cd.RequiredSignals {
		if _, ok := signals[sig]; !ok {
			return false
		}
	}
	return true
}

// skippedDependency returns the first dependency of cd that was skipped,
// if any, so the batch path can cascade the skip to dependents.
func skippedDependency(cd *health.CheckDefinition, skipped map[string]bool) (string, bool) {
	for _, dep := range cd.Dependencies {
		if skipped[dep] {
			return dep, true
		}
	}
	return "", false
}

func noApplicableDetail(cd *health.CheckDefinition) string {
	return fmt.Sprintf("no supplied measurement carries the required signals %v", cd.RequiredSignals)
}

// checkLevelSkip decides the measurement-independent skips: an operator
// disable, an architecture or execution-context mismatch, or a required
// threshold the operator never supplied. These depend only on the check
// and the evaluation context, not on any measurement, so the batch path
// decides them once per check rather than once per routed measurement.
// It returns the skip verdict and true when the check must not run.
func checkLevelSkip(cd *health.CheckDefinition, p Params, now time.Time) (health.CheckVerdict, bool) {
	if p.Disabled {
		return skippedVerdict(cd, now, health.CheckDisabled, "disabled by operator configuration"), true
	}

	if p.Architecture != "" && len(cd.Architectures) > 0 && !slices.Contains(cd.Architectures, p.Architecture) {
		return skippedVerdict(cd, now, health.ArchitectureUnsupported,
			fmt.Sprintf("check requires %v, running on %s", cd.Architectures, p.Architecture)), true
	}

	if len(cd.ExecutionContexts) > 0 && !slices.Contains(cd.ExecutionContexts, p.ExecutionContext) {
		return skippedVerdict(cd, now, health.DeploymentUnsupported,
			fmt.Sprintf("check requires %v, running in %s", cd.ExecutionContexts, p.ExecutionContext)), true
	}

	// A threshold the operator must supply has no usable catalog value, so
	// the check cannot run until one arrives. Skip rather than evaluate: a
	// configuration gap is not a node fault.
	var unconfigured []string
	for _, name := range requiredThresholds(cd.Thresholds) {
		if _, ok := p.Overrides[name]; !ok {
			unconfigured = append(unconfigured, name)
		}
	}
	if len(unconfigured) > 0 {
		return skippedVerdict(cd, now, health.ThresholdUnconfigured,
			fmt.Sprintf("required thresholds not configured: %s", strings.Join(unconfigured, ", "))), true
	}

	return health.CheckVerdict{}, false
}

// evaluateAgainst runs a routed check against one measurement's signals
// and error list. The caller has already applied [checkLevelSkip] and
// confirmed the measurement [routes] to the check, so this is the pure
// data path.
func (e *EvaluationEngine) evaluateAgainst(cd *health.CheckDefinition, signals map[string]any, errs []health.ErroredMeasurement, p Params, now time.Time) (health.CheckVerdict, error) {
	// If any of the sensor's per-field failures cover a signal the
	// check requires, fail the check with a Critical verdict rather
	// than running CEL against a zero value where a real measurement
	// should be. The verdict's detail joins every error entry that
	// touched one of this check's required signals so the message
	// names exactly what went wrong. Checks whose required signals
	// were all collected fall through to normal evaluation against
	// the partial data.
	if detail, action := matchingError(errs, cd.RequiredSignals); detail != "" {
		// Observation failure: the data was unavailable, so the rules never
		// ran. The verdict carries a collection-failure action, never the
		// check's hardware-remediation default (that is for matched rules).
		return ErroredVerdict(cd, now, detail, action), nil
	}

	resolved, err := health.ResolveThresholds(cd.ID, cd.Thresholds, p.Overrides, p.Architecture)
	if err != nil {
		return health.CheckVerdict{}, err
	}

	// Merge signals and resolved threshold values into a flat map
	// for CEL evaluation.
	vars := make(map[string]any, len(signals)+len(resolved))
	for k, v := range signals {
		vars[k] = v
	}
	for k, rt := range resolved {
		vars[k] = rt.Value
	}

	// Evaluate every rule and aggregate the matches. Severity comes from
	// the worst (largest) value across matching rules; Messages preserves
	// declaration order so consumers see the rules' findings in the order
	// they were authored. RecommendedAction comes from the
	// highest-priority matching rule (rank: severity desc, then
	// declaration order asc) — its override if non-empty, else the check
	// default.
	var (
		messages         []string
		severity         = health.Pass
		topSeverity      health.Severity
		topRuleAction    health.RecommendedAction
		topRuleHasAction bool
		anyMatched       bool
	)

	for _, rule := range cd.Rules {
		matched, err := e.cel.Eval(rule.Expression, vars)
		if err != nil {
			return health.CheckVerdict{}, fmt.Errorf("check %q rule %q: %w", cd.ID, rule.Expression, err)
		}
		if !matched {
			continue
		}

		messages = append(messages, interpolateMessage(rule.Message, vars))
		severity = health.Worse(severity, rule.Severity)

		if !anyMatched || rule.Severity > topSeverity {
			topSeverity = rule.Severity
			topRuleAction = rule.RecommendedAction
			topRuleHasAction = rule.HasRecommendedAction()
		}
		anyMatched = true
	}

	action := cd.RecommendedAction
	if topRuleHasAction {
		action = topRuleAction
	}

	if !anyMatched {
		// No rule matched; check passes.
		return health.CheckVerdict{
			CheckID:        cd.ID,
			Severity:       health.Pass,
			Category:       cd.Category,
			Thresholds:     resolved,
			ObservedValues: signals,
			MaturityLevel:  cd.MaturityLevel,
			NVIDIAEndorsed: cd.Provenance == health.NVIDIA,
			Timestamp:      now,
		}, nil
	}

	return health.CheckVerdict{
		CheckID:           cd.ID,
		Severity:          severity,
		Messages:          messages,
		Category:          cd.Category,
		Thresholds:        resolved,
		ObservedValues:    signals,
		RecommendedAction: action,
		MaturityLevel:     cd.MaturityLevel,
		NVIDIAEndorsed:    cd.Provenance == health.NVIDIA,
		Timestamp:         now,
	}, nil
}

// matchingError inspects the error entries whose FailedSignals overlap the
// check's required signals. It returns a joined detail string of their
// causes ("" when none match) and the recommended action for the resulting
// Critical verdict.
//
// These are observation failures, not observed faults — the CEL rules
// never ran — so the action is never the check's hardware-remediation
// default. A collection timeout maps to retry_collection; any other
// collection failure (a driver, permission, or decode error) maps to
// check_collection: investigate the collection rather than remediate
// hardware. A non-timeout failure outranks a timeout when both are
// present, since a concrete collection error is more actionable than a
// slow read.
func matchingError(errs []health.ErroredMeasurement, required []string) (detail string, action health.RecommendedAction) {
	if len(errs) == 0 {
		return "", health.NoAction
	}
	var details []string
	var hasTimeout, hasOther bool
	for _, e := range errs {
		for _, sig := range required {
			if slices.Contains(e.FailedSignals(), sig) {
				details = append(details, e.Err().Error())
				if errors.Is(e.Err(), health.ErrCollectionTimeout) {
					hasTimeout = true
				} else {
					hasOther = true
				}
				break
			}
		}
	}
	switch {
	case hasOther:
		action = health.Drain(health.DiagnosticActionCheckCollection)
	case hasTimeout:
		action = health.Drain(health.DiagnosticActionRetryCollection)
	}
	return strings.Join(details, "; "), action
}

// requiredThresholds returns the names of specs the operator must supply,
// sorted so the reported order is deterministic.
func requiredThresholds(specs map[string]health.ThresholdSpec) []string {
	var names []string
	for name, spec := range specs {
		if spec.RequiredOverride {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func skippedVerdict(cd *health.CheckDefinition, ts time.Time, reason health.SkipReason, detail string) health.CheckVerdict {
	return health.CheckVerdict{
		CheckID:        cd.ID,
		Skipped:        true,
		SkipReason:     reason,
		SkipDetail:     detail,
		Category:       cd.Category,
		MaturityLevel:  cd.MaturityLevel,
		NVIDIAEndorsed: cd.Provenance == health.NVIDIA,
		Timestamp:      ts,
	}
}

// ErroredVerdict builds a Critical CheckVerdict for a check whose input
// measurement could not be observed — the engine emits one when a sensor's
// errored measurement satisfies the check's schema (architecture, context,
// RequiredSignals) but the data is unavailable. action is the
// collection-failure remediation; it is never the check's fault default,
// which applies only when a rule actually matched.
func ErroredVerdict(cd *health.CheckDefinition, ts time.Time, detail string, action health.RecommendedAction) health.CheckVerdict {
	return health.CheckVerdict{
		CheckID:           cd.ID,
		Severity:          health.Critical,
		Messages:          []string{fmt.Sprintf("measurement unavailable: %s", detail)},
		Category:          cd.Category,
		MaturityLevel:     cd.MaturityLevel,
		NVIDIAEndorsed:    cd.Provenance == health.NVIDIA,
		RecommendedAction: action,
		Timestamp:         ts,
	}
}

func interpolateMessage(template string, vars map[string]any) string {
	result := template
	for name, val := range vars {
		result = strings.ReplaceAll(result, "${"+name+"}", fmt.Sprint(val))
	}
	return result
}

// TopologicalOrder returns check IDs ordered so that dependencies come
// before the checks that depend on them. IDs are sorted before traversal
// for deterministic output.
func TopologicalOrder(checks map[string]*health.CheckDefinition) []string {
	visited := make(map[string]bool, len(checks))
	var order []string

	ids := make([]string, 0, len(checks))
	for id := range checks {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		if cd, ok := checks[id]; ok {
			for _, dep := range cd.Dependencies {
				visit(dep)
			}
		}
		order = append(order, id)
	}

	for _, id := range ids {
		visit(id)
	}
	return order
}
