// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/celeval"
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
// is supplied via the Measurement passed to EvaluateCheck and the batch
// path, not through this struct.
type Params struct {
	Overrides        map[string]any
	Cadence          *health.SchedulingCadence
	Architecture     string
	ExecutionContext health.ExecutionContext
	Disabled         bool
}

// EvaluateCheck runs a single check against one measurement.
//
// The measurement-independent skips apply first (disabled, cadence,
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
	o := observe(m)
	if !routes(cd, o.category, o.signals) {
		return skippedVerdict(cd, now, health.NoApplicableMeasurement, noApplicableDetail(cd)), nil
	}
	if err := checkEntities(cd, []observed{o}); err != nil {
		return health.CheckVerdict{}, err
	}
	return e.evaluateAgainst(cd, o.signals, o.errs, o.entity, p, now)
}

// observed is one measurement with everything evaluation reads from it
// extracted once. Fields are ordered for nogo's pointer-scan metric.
type observed struct {
	m        health.Measurement
	signals  map[string]any
	entity   string
	key      health.EntityKey
	errs     []health.ErroredMeasurement
	category health.Category
}

func observe(m health.Measurement) observed {
	return observed{
		m: m, signals: ToSignals(m), entity: entityID(m),
		key: health.KeyOf(m), errs: m.Errors(), category: m.Category(),
	}
}

// EvaluateBatch routes every measurement to the checks it can feed and
// returns the flat verdict list before node-level aggregation.
//
// A measurement feeds a check when they share a Category and the
// measurement's schema carries all of the check's RequiredSignals (see
// [routes]). Each check is evaluated once per measurement that feeds it,
// so a per-entity check yields one verdict per entity, and exactly once as
// a [health.NoApplicableMeasurement] skip when its category was collected
// but no measurement carried its required signals. The
// measurement-independent skips (disabled, cadence, architecture, context,
// unconfigured threshold) are decided once per check, not once per
// measurement. A dependency is not among them: it is examined per
// measurement, and only to name the cause on a verdict for an entity this
// check could not read at all. See [lostEntity].
//
// Before evaluating anything, every check that will run is given at most one
// measurement per [health.EntityKey]; see [checkEntities]. Otherwise the batch
// returns a [health.AmbiguousMeasurementError] and no verdicts.
//
// A check whose Category supplied no measurement at all skips
// [health.SensorMissing]. Registering a check is what declares the
// expectation: if the catalog holds a GPU check, this run means to assess
// GPUs, so a GPU sensor that is absent, unconfigured, or never constructed
// is a hole in the assessment rather than a domain that was out of scope.
// Every registered check therefore yields a verdict — a result or a skip —
// and none can vanish. NoApplicableMeasurement remains the sharper case:
// the domain was collected but this check's measurement shape was not
// among what the sensor produced.
//
// The skip is a report, not a failure: skipped checks are excluded from
// OverallSeverity, so a missing sensor never turns a node CRITICAL on its
// own. Recording it is the point — the check stays visible as unassessed
// under a skip_reason a consumer can act on. The measurement-independent
// skips are decided first, so an operator's [health.CheckDisabled]
// outranks SensorMissing:
// a check turned off on purpose never reads as a sensor hole.
//
// paramsFor supplies the per-check evaluation context; the engine asks
// for fresh Params per check so overrides and disabled state stay
// per-check, matching the single-check EvaluateCheck path.
func (e *EvaluationEngine) EvaluateBatch(checks map[string]*health.CheckDefinition, measurements []health.Measurement, paramsFor func(checkID string) Params) ([]health.CheckVerdict, error) {
	now := time.Now()

	// Extract each measurement's signals once; every check reuses them.
	obs := make([]observed, len(measurements))
	presentCategories := make(map[health.Category]bool)
	for i, m := range measurements {
		obs[i] = observe(m)
		presentCategories[obs[i].category] = true
	}

	order := TopologicalOrder(checks)
	// Decide each check's params and measurement-independent skip once; the
	// validation pass and the evaluation loop both read them.
	params := make(map[string]Params, len(order))
	skips := make(map[string]health.CheckVerdict)
	for _, id := range order {
		params[id] = paramsFor(id)
		if v, isSkip := checkLevelSkip(checks[id], params[id], now); isSkip {
			skips[id] = v
		}
	}

	// Validate every check's input before evaluating any, so an ambiguous
	// run returns no verdicts rather than a partial set.
	for _, id := range order {
		cd := checks[id]
		if _, isSkip := skips[id]; isSkip {
			continue
		}
		var routed []observed
		for _, o := range obs {
			if routes(cd, o.category, o.signals) {
				routed = append(routed, o)
			}
		}
		if err := checkEntities(cd, routed); err != nil {
			return nil, err
		}
	}

	verdicts := make([]health.CheckVerdict, 0, len(order))
	failed := make(map[string]health.Severity, len(order))

	for _, id := range order {
		cd := checks[id]

		p := params[id]
		if v, isSkip := skips[id]; isSkip {
			verdicts = append(verdicts, v)
			continue
		}

		// Nothing of this check's domain arrived. Record the hole so the
		// check is visibly unassessed instead of silently producing no
		// verdict.
		if !presentCategories[cd.Category] {
			verdicts = append(verdicts, skippedVerdict(cd, now, health.SensorMissing,
				fmt.Sprintf("no measurement of category %s was supplied", cd.Category)))
			continue
		}

		matched := false
		for _, o := range obs {
			if !routes(cd, o.category, o.signals) {
				continue
			}
			matched = true

			v, err := e.evaluateAgainst(cd, o.signals, o.errs, o.entity, p, now)
			if err != nil {
				return nil, fmt.Errorf("evaluating %q: %w", id, err)
			}
			// Two conditions, both required. ObservedValues separates a matched
			// rule, which always records it, from [ErroredVerdict], which never
			// does: a dependency that went Critical because its own measurement
			// could not be collected observed no fault, so it explains nothing
			// about a sibling that also could not be read. The comparison is
			// >= so [health.Unknown], which ranks above Critical and means the
			// check could not complete, is not the one severity excluded from
			// explaining anything.
			if v.Severity >= health.Critical && v.ObservedValues != nil {
				failed[id] = v.Severity
			}

			// This entity could not be read at all and a dependency already
			// reported Critical, which is the fault this verdict restates. Name
			// it so the operator reads one cause instead of correlating several
			// identical reports. The wording claims only that the dependency is
			// Critical: it may be so because its own measurement errored, which
			// is a different fact from an observed fault.
			//
			// The cause extends the existing message instead of adding a second
			// one. A consumer classifies a collection failure by every message
			// carrying the "measurement unavailable" prefix, so a second message
			// without it reads as an observed fault and draws hardware
			// remediation rather than a collection check.
			if dep, sev, ok := failedDependency(cd, failed); ok && lostEntity(o.errs, o.signals) {
				if n := len(v.Messages); n > 0 {
					v.Messages[n-1] += fmt.Sprintf("; explained by dependency %q, itself %s", dep, sev)
				}
			}
			verdicts = append(verdicts, v)
		}
		if !matched {
			verdicts = append(verdicts, skippedVerdict(cd, now, health.NoApplicableMeasurement,
				noApplicableDetail(cd)))
		}
	}

	return verdicts, nil
}

// checkEntities rejects the measurements routed to cd unless each describes a
// distinct, validly keyed entity, and each provisional or unidentified one
// reports a failure of a signal cd reads. Concrete types do not matter: two
// providers' types that both route to cd for one entity are duplicates.
//
// A provisional key names a source-local slot or a whole source, which no
// other source can be joined to, and an unidentified one names nothing at all.
// Values under either could describe a GPU another provider also reports, so
// such a measurement may only carry the failure that left it unidentified.
func checkEntities(cd *health.CheckDefinition, routed []observed) error {
	ambiguous := func(key health.EntityKey, reason string, ms ...observed) error {
		types := make([]string, len(ms))
		for i, o := range ms {
			types[i] = fmt.Sprintf("%T", o.m)
		}
		return &health.AmbiguousMeasurementError{CheckID: cd.ID, Entity: key, Reason: reason, Types: types}
	}
	seen := make(map[health.EntityKey]observed, len(routed))
	for _, o := range routed {
		if err := o.key.Validate(); err != nil {
			return ambiguous(o.key, err.Error(), o)
		}
		if o.key.Provisional() && !reportsFailure(cd, o) {
			return ambiguous(o.key, "a provisional identity carries collected values; joining them to a GPU requires a verified ID", o)
		}
		if o.key.Unidentified() && !reportsFailure(cd, o) {
			return ambiguous(o.key, "collected values name no entity, so they may duplicate another provider's; supply a verified ID", o)
		}
		if prev, dup := seen[o.key]; dup {
			reason := "more than one measurement for one entity"
			if o.key.Unidentified() {
				reason = "more than one measurement names no entity, so they cannot be told apart"
			}
			return ambiguous(o.key, reason, prev, o)
		}
		seen[o.key] = o
	}
	return nil
}

// reportsFailure reports whether o's errors fail a signal cd reads. A check
// that requires no signals reads them all, so o must have lost every signal it
// carries; a partial failure would leave values that check can read.
func reportsFailure(cd *health.CheckDefinition, o observed) bool {
	if len(cd.RequiredSignals) == 0 {
		return lostEntity(o.errs, o.signals)
	}
	detail, _ := matchingError(o.errs, cd.RequiredSignals)
	return detail != ""
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

// lostEntity reports whether the collection errors account for every signal the
// measurement carries, which is the signature a sensor emits when it could not
// read the entity at all.
//
// This is what separates a device that vanished from the enumeration, where a
// Critical dependency genuinely explains this check too, from read failures on
// a device that enumerated fine, even ones that together cover every signal
// this check requires. Those are independent faults, and naming the dependency
// on them would credit an unrelated cause.
func lostEntity(errs []health.ErroredMeasurement, signals map[string]any) bool {
	if len(errs) == 0 || len(signals) == 0 {
		return false
	}
	for sig := range signals {
		var covered bool
		for _, e := range errs {
			if slices.Contains(e.FailedSignals(), sig) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// failedDependency returns the first dependency of cd that evaluated Critical or
// worse, with the severity it reported, so the batch path can name it as the
// cause on a verdict that only restates it.
func failedDependency(cd *health.CheckDefinition, failed map[string]health.Severity) (string, health.Severity, bool) {
	for _, dep := range cd.Dependencies {
		if sev, ok := failed[dep]; ok {
			return dep, sev, true
		}
	}
	return "", health.Pass, false
}

func noApplicableDetail(cd *health.CheckDefinition) string {
	return fmt.Sprintf("no supplied measurement carries the required signals %v", cd.RequiredSignals)
}

// checkLevelSkip decides the measurement-independent skips: an operator
// disable, cadence, architecture or execution-context mismatch, or a required
// threshold the operator never supplied. These depend only on the check and
// evaluation context, not on any measurement, so the batch path decides them
// once per check rather than once per routed measurement. It returns the skip
// verdict and true when the check must not run.
func checkLevelSkip(cd *health.CheckDefinition, p Params, now time.Time) (health.CheckVerdict, bool) {
	if p.Disabled {
		return skippedVerdict(cd, now, health.CheckDisabled, "disabled by operator configuration"), true
	}

	if p.Cadence != nil && !cd.SchedulingCadence.Covers(*p.Cadence) {
		return skippedVerdict(cd, now, health.CadenceMismatch,
			fmt.Sprintf("check cadence %s does not cover requested cadence %s", cd.SchedulingCadence, *p.Cadence)), true
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
// data path. entity names the measurement's subject (see
// [health.EntityIdentifier]) and is empty for host-wide measurements.
func (e *EvaluationEngine) evaluateAgainst(cd *health.CheckDefinition, signals map[string]any, errs []health.ErroredMeasurement, entity string, p Params, now time.Time) (health.CheckVerdict, error) {
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
		return withEntity(ErroredVerdict(cd, now, detail, action), entity), nil
	}

	resolved, err := resolveThresholds(cd.ID, cd.Thresholds, p.Overrides, p.Architecture)
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

	observed := observedValues(cd, signals)
	if !anyMatched {
		// No rule matched; check passes.
		v := verdictFor(cd, now)
		v.Severity = health.Pass
		v.Thresholds = resolved
		v.ObservedValues = observed
		return v, nil
	}

	v := verdictFor(cd, now)
	v.Severity = severity
	v.Messages = messages
	v.Thresholds = resolved
	v.ObservedValues = observed
	// action's slice belongs to the registered definition; the verdict goes to
	// callers.
	action.RemediationActions = slices.Clone(action.RemediationActions)
	v.RecommendedAction = action
	return withEntity(v, entity), nil
}

// observedValues limits signals to the ones cd requires. A sibling field the
// check never read may be unfed (narrowed collection, unmapped or failed
// signal), and its zero value would read as a real measurement. A check that
// declares no required signals can read any field, so it keeps them all.
func observedValues(cd *health.CheckDefinition, signals map[string]any) map[string]any {
	if len(cd.RequiredSignals) == 0 {
		return signals
	}
	out := make(map[string]any, len(cd.RequiredSignals))
	for _, name := range cd.RequiredSignals {
		if v, ok := signals[name]; ok {
			out[name] = v
		}
	}
	return out
}

// entityID returns the measurement's subject, or "" when the type does not
// identify one (host-wide measurements) or the sensor could not determine it.
func entityID(m health.Measurement) string {
	id, ok := m.(health.EntityIdentifier)
	if !ok {
		return ""
	}
	return id.EntityID()
}

// withEntity names the entity a non-PASS verdict was reached against. A
// passing verdict names nothing: AffectedEntities is the set a consumer
// should act on, and every healthy entity in it would be noise.
func withEntity(v health.CheckVerdict, entity string) health.CheckVerdict {
	if entity != "" && v.Severity > health.Pass {
		v.AffectedEntities = []string{entity}
	}
	return v
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
		action = health.Drain(health.RemediationActionCheckCollection)
	case hasTimeout:
		action = health.Drain(health.RemediationActionRetryCollection)
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

// verdictFor seeds the fields every verdict copies from its check definition.
// The pass, matched, skipped, and errored paths all build from it, so a field
// added here cannot land on results but not skips.
func verdictFor(cd *health.CheckDefinition, ts time.Time) health.CheckVerdict {
	return health.CheckVerdict{
		CheckID:        cd.ID,
		Category:       cd.Category,
		ConditionScope: cd.ConditionScope,
		MaturityLevel:  cd.MaturityLevel,
		NVIDIAEndorsed: cd.Provenance == health.NVIDIA,
		Timestamp:      ts,
	}
}

func skippedVerdict(cd *health.CheckDefinition, ts time.Time, reason health.SkipReason, detail string) health.CheckVerdict {
	v := verdictFor(cd, ts)
	v.Skipped = true
	v.SkipReason = reason
	v.SkipDetail = detail
	return v
}

// ErroredVerdict builds a Critical CheckVerdict for a check whose input
// measurement could not be observed — the engine emits one when a sensor's
// errored measurement satisfies the check's schema (architecture, context,
// RequiredSignals) but the data is unavailable. action is the
// collection-failure remediation and the scope is [health.NodeScope]; neither
// is the check's declared value, which applies only when a rule actually
// matched.
func ErroredVerdict(cd *health.CheckDefinition, ts time.Time, detail string, action health.RecommendedAction) health.CheckVerdict {
	v := verdictFor(cd, ts)
	v.Severity = health.Critical
	v.Messages = []string{fmt.Sprintf("measurement unavailable: %s", detail)}
	v.RecommendedAction = action
	// The rules never ran, so nothing beyond this node was observed. Like the
	// action above, the scope reports the local sensor failure that happened,
	// not the blast radius this check carries when it detects a real condition.
	v.ConditionScope = health.NodeScope
	return v
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
// for deterministic output. A dependency naming an unregistered check is
// skipped: a consumer may register any subset of the catalog, so the edge is
// simply inactive.
func TopologicalOrder(checks map[string]*health.CheckDefinition) []string {
	visited := make(map[string]bool, len(checks))
	order := make([]string, 0, len(checks))

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
		for _, dep := range checks[id].Dependencies {
			if _, ok := checks[dep]; ok {
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
