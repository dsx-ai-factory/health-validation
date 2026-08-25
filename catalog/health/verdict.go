package health

import (
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/version"
	"github.com/google/uuid"
)

// newID returns a random RFC 4122 version-4 UUID string. It backs the per-run
// trace_id and per-check span_id so a downstream system can trace a
// specific check execution and correlate all checks fired in one run.
func newID() string {
	return uuid.NewString()
}

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
	SpanID            string                       `json:"span_id"`  // Unique per check invocation.
	TraceID           string                       `json:"trace_id"` // Shared by all checks in the same run.
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
	CatalogVersion     string                `json:"catalog_version"` // Catalog module version compiled into the producer.
	Timestamp          time.Time             `json:"timestamp"`
	NodeID             string                `json:"node_id,omitempty"` // Opaque identifier for the node under test.
	TraceID            string                `json:"trace_id"`          // Shared by all checks in this run; echoed on each CheckVerdict.
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

// AggregateNodeVerdict builds a NodeVerdict from a set of check verdicts,
// applying the given completion threshold. If the ratio of completed
// checks to expected checks falls below the threshold, the overall
// severity is Unknown.
func AggregateNodeVerdict(verdicts []CheckVerdict, completionThreshold float64) NodeVerdict {
	now := time.Now()
	traceID := newID()
	nv := NodeVerdict{
		SchemaVersion:    NodeVerdictSchemaVersion,
		CatalogVersion:   version.Version(),
		TraceID:          traceID,
		OverallSeverity:  Pass,
		SeverityByDomain: make(map[Category]Severity),
		Timestamp:        now,
	}

	var completed, total int
	domainChecks := make(map[Category][]string)
	domainEntities := make(map[Category][]string)

	for _, v := range verdicts {
		total++
		// Stamp a unique id on every check invocation and the shared trace
		// id on each, so downstream consumers can trace one check execution
		// and correlate co-fired checks even when records are split apart.
		v.SpanID = newID()
		v.TraceID = traceID
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

	if completionThreshold > 0 && nv.CompletionRatio < completionThreshold {
		nv.OverallSeverity = Unknown
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
