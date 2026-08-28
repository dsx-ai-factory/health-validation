package health

import (
	"fmt"
	"slices"
	"time"
)

// CheckDefinition is the canonical shape of a health check in the catalog.
// Each definition encodes NVIDIA's operational judgment about a specific
// hardware or driver condition: which signals to examine, what thresholds
// apply, and what action to recommend.
//
// Check definitions are data. They carry no sensor or hardware dependencies.
// Evaluation happens when a caller provides signal values to [Catalog.Evaluate].
type CheckDefinition struct {
	Thresholds        map[string]ThresholdSpec `json:"thresholds"`
	FeasibilityClass  string                   `json:"feasibility_class,omitempty"`
	Description       string                   `json:"description"`
	Name              string                   `json:"name"`
	ID                string                   `json:"id"`
	RequiredSignals   []string                 `json:"required_signals"`
	Dependencies      []string                 `json:"dependencies,omitempty"`
	Rules             []Rule                   `json:"rules"`
	Architectures     []string                 `json:"architectures,omitempty"`
	ExecutionContexts []ExecutionContext       `json:"execution_contexts"`
	Prerequisites     []Prerequisite           `json:"prerequisites,omitempty"`
	Timeout           time.Duration            `json:"timeout,omitempty"`
	MaturityLevel     MaturityLevel            `json:"maturity_level"`
	SchedulingCadence SchedulingCadence        `json:"scheduling_cadence"`
	Category          Category                 `json:"category"`
	Provenance        Provenance               `json:"provenance"`
	ExecutionClass    ExecutionClass           `json:"execution_class"`
	RecommendedAction RecommendedAction        `json:"recommended_action,omitzero"`
}

// Validate reports whether the definition is internally consistent. Names are
// sorted so the reported threshold is deterministic.
//
// A Fixed threshold cannot require an override (the check would skip without
// one and reject one). When PerArchitecture is set, every architecture in
// Architectures must be a key. Value may be omitted only when that coverage
// is complete; unrestricted checks (empty Architectures) still need a Value.
func (cd CheckDefinition) Validate() error {
	names := make([]string, 0, len(cd.Thresholds))
	for name := range cd.Thresholds {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		spec := cd.Thresholds[name]
		if spec.RequiredOverride && spec.Category == FixedThreshold {
			return fmt.Errorf("check %q threshold %q: a fixed threshold cannot require an override", cd.ID, name)
		}
		if err := validateThresholdCoverage(cd.ID, name, spec, cd.Architectures); err != nil {
			return err
		}
	}
	return nil
}

// validateThresholdCoverage requires PerArchitecture to cover Architectures, or else Value / RequiredOverride.
func validateThresholdCoverage(checkID, name string, spec ThresholdSpec, architectures []string) error {
	if len(spec.PerArchitecture) > 0 && len(architectures) > 0 {
		missing := make([]string, 0)
		for _, arch := range architectures {
			if _, ok := spec.PerArchitecture[arch]; !ok {
				missing = append(missing, arch)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return fmt.Errorf("check %q threshold %q: PerArchitecture missing architectures %v", checkID, name, missing)
		}
		return nil
	}
	if spec.RequiredOverride || spec.Value != nil {
		return nil
	}
	return fmt.Errorf("check %q threshold %q: Value is required unless PerArchitecture covers every declared architecture", checkID, name)
}

// Rule defines a single evaluation condition within a check. Rules are
// evaluated in declaration order; the first rule whose CEL expression
// evaluates to true determines the verdict.
type Rule struct {
	// Expression is a CEL expression that evaluates to a boolean.
	// Variables available are the union of signal field names and
	// threshold names from the parent CheckDefinition.
	Expression string `json:"expression"`

	// Message describes the detected condition. May contain ${variable}
	// placeholders interpolated from signal and threshold values.
	Message string `json:"message"`

	// Severity produced when this rule matches.
	Severity Severity `json:"severity"`

	// RecommendedAction, when non-zero (see
	// [RecommendedAction.IsZero]), overrides the parent
	// [CheckDefinition.RecommendedAction] in the resulting
	// [CheckVerdict]. Use it when a specific failure mode warrants a
	// different remediation than the check's default. A zero value
	// leaves the check-level action in place.
	RecommendedAction RecommendedAction `json:"recommended_action,omitzero"`
}

// HasRecommendedAction reports whether the rule sets a non-zero
// RecommendedAction that should override the parent
// [CheckDefinition.RecommendedAction].
func (r Rule) HasRecommendedAction() bool {
	return !r.RecommendedAction.IsZero()
}

// Prerequisite declares a minimum software requirement for a check.
type Prerequisite struct {
	Name       string `json:"name"`
	MinVersion string `json:"min_version"`
}
