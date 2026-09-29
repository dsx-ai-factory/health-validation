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
	Messages          []string                     `json:"messages,omitempty"`
	AffectedEntities  []string                     `json:"affected_entities,omitempty"`
	RecommendedAction RecommendedAction            `json:"recommended_action,omitzero"`
	Severity          Severity                     `json:"severity"`
	Category          Category                     `json:"category"`
	ConditionScope    ConditionScope               `json:"condition_scope"` // Blast radius; NODE when collection failed.
	SkipReason        SkipReason                   `json:"skip_reason,omitempty"`
	MaturityLevel     MaturityLevel                `json:"maturity_level"`
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
const NodeVerdictSchemaVersion = "0.3"

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
	OverallSeverity    Severity              `json:"overall_severity"`
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
// Skipped checks are recorded separately in SkippedChecks and do not
// contribute to the overall severity. No coverage ratio is derived: most
// skips are by design (a check that does not apply to this architecture or
// execution context), so a count of them says nothing about how well the
// node was assessed. Consumers that care about coverage should inspect
// SkippedChecks and their SkipReason.
func AggregateNodeVerdict(verdicts []CheckVerdict) NodeVerdict {
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

	domainChecks := make(map[Category][]string)
	domainEntities := make(map[Category][]string)

	for _, v := range verdicts {
		// Stamp a unique id on every check invocation and the shared trace
		// id on each, so downstream consumers can trace one check execution
		// and correlate co-fired checks even when records are split apart.
		v.SpanID = newID()
		v.TraceID = traceID
		if v.Skipped {
			nv.SkippedChecks = append(nv.SkippedChecks, v)
			continue
		}

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
