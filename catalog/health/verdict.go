package health

import "time"

// CheckVerdict is the outcome of evaluating a single check against
// signal values. It carries all context needed for downstream systems
// to make remediation decisions without referencing catalog source.
//
// Every Rule whose expression matched contributes one entry to
// Messages. Severity is the most severe across the matching rules;
// RecommendedAction comes from the highest-priority matching rule's
// override (or the check default when no override applies).
type CheckVerdict struct {
	Timestamp         time.Time                    `json:"timestamp"`
	ObservedValues    map[string]any               `json:"observed_values,omitempty"`
	Thresholds        map[string]ResolvedThreshold `json:"thresholds,omitempty"`
	CheckID           string                       `json:"check_id"`
	SkipDetail        string                       `json:"skip_detail,omitempty"`
	AffectedEntities  []string                     `json:"affected_entities,omitempty"`
	Messages          []string                     `json:"messages,omitempty"`
	Severity          Severity                     `json:"severity"`
	Category          Category                     `json:"category"`
	SkipReason        SkipReason                   `json:"skip_reason,omitempty"`
	MaturityLevel     MaturityLevel                `json:"maturity_level"`
	RecommendedAction RecommendedAction            `json:"recommended_action,omitzero"`
	Skipped           bool                         `json:"skipped"`
	NVIDIAEndorsed    bool                         `json:"nvidia_endorsed"`
}

// NodeVerdictSchemaVersion is the current JSON schema version for
// [NodeVerdict], using MAJOR.MINOR versioning. Downstream consumers must
// check this field before acting on a verdict; an unrecognised or missing
// value must be treated as untrusted output. Increment MAJOR on any breaking
// change to the NodeVerdict JSON shape (removed fields, renamed fields,
// changed semantics); increment MINOR for additive fields, enum values, or
// outcomes. When this changes, update catalog/health/node-verdict.schema.json to match.
const NodeVerdictSchemaVersion = "0.1"

// NodeVerdict aggregates per-check verdicts into a node-level
// health assessment.
type NodeVerdict struct {
	SchemaVersion      string                `json:"schema_version"`
	Timestamp          time.Time             `json:"timestamp"`
	NodeID             string                `json:"node_id,omitempty"` // Opaque identifier for the node under test.
	SeverityByDomain   map[Category]Severity `json:"severity_by_domain"`
	AffectedComponents []AffectedComponent   `json:"affected_components,omitempty"`
	CheckResults       []CheckVerdict        `json:"check_results"`
	SkippedChecks      []CheckVerdict        `json:"skipped_checks,omitempty"`
	RecommendedAction  RecommendedAction     `json:"recommended_action,omitzero"`
	CompletionRatio    float64               `json:"completion_ratio"`
	OverallSeverity    Severity              `json:"overall_severity"`
}

// pickNodeAction selects the single node-level RecommendedAction from
// the checks that tie at the top severity. Among those candidates the
// most urgent action wins: highest OperationalState first, then the
// highest priority DiagnosticAction; see [RecommendedAction.outranks]. Returns
// NoAction when no check matches top (e.g. all Pass).
func pickNodeAction(verdicts []CheckVerdict, top Severity) RecommendedAction {
	var action RecommendedAction
	for _, v := range verdicts {
		if v.Severity != top {
			continue
		}
		if v.RecommendedAction.outranks(action) {
			action = v.RecommendedAction
		}
	}
	return action
}

// AffectedComponent summarizes a category with non-Pass results.
type AffectedComponent struct {
	CheckIDs    []string `json:"check_ids"`
	Entities    []string `json:"entities,omitempty"`
	Category    Category `json:"category"`
	MaxSeverity Severity `json:"max_severity"`
}

// ExitCode returns the process exit code for this verdict:
//
//	0: all checks passed or INFO only
//	1: at least one CRITICAL
//	2: tool failure / could not run
//	3: at least one WARNING, no CRITICAL
func (nv *NodeVerdict) ExitCode() int {
	switch nv.OverallSeverity {
	case Pass, Info:
		return 0
	case Critical:
		return 1
	case Unknown:
		return 2
	case Warning:
		return 3
	default:
		return 2
	}
}

// AggregateNodeVerdict builds a NodeVerdict from a set of check verdicts.
// Skipped checks are recorded separately and do not contribute to the
// overall severity; CompletionRatio reports how many of the evaluated
// checks completed, for observability only.
func AggregateNodeVerdict(verdicts []CheckVerdict) NodeVerdict {
	now := time.Now()
	nv := NodeVerdict{
		SchemaVersion:    NodeVerdictSchemaVersion,
		OverallSeverity:  Pass,
		SeverityByDomain: make(map[Category]Severity),
		Timestamp:        now,
	}

	var completed, total int
	domainChecks := make(map[Category][]string)
	domainEntities := make(map[Category][]string)

	for _, v := range verdicts {
		total++
		if v.Skipped {
			nv.SkippedChecks = append(nv.SkippedChecks, v)
			continue
		}

		completed++
		nv.CheckResults = append(nv.CheckResults, v)
		nv.OverallSeverity = Worse(nv.OverallSeverity, v.Severity)

		nv.SeverityByDomain[v.Category] = Worse(
			nv.SeverityByDomain[v.Category], v.Severity,
		)

		if v.Severity > Pass {
			domainChecks[v.Category] = append(domainChecks[v.Category], v.CheckID)
			domainEntities[v.Category] = append(domainEntities[v.Category], v.AffectedEntities...)
		}
	}

	if total > 0 {
		nv.CompletionRatio = float64(completed) / float64(total)
	}

	// The node-level action mirrors severity: it comes from the checks
	// that tie at OverallSeverity, so the surfaced remediation always
	// belongs to a check at the top tier.
	nv.RecommendedAction = pickNodeAction(nv.CheckResults, nv.OverallSeverity)

	for domain, sev := range nv.SeverityByDomain {
		if sev > Pass {
			nv.AffectedComponents = append(nv.AffectedComponents, AffectedComponent{
				Category:    domain,
				MaxSeverity: sev,
				CheckIDs:    domainChecks[domain],
				Entities:    domainEntities[domain],
			})
		}
	}

	return nv
}
