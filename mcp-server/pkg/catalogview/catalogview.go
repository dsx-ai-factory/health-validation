// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package catalogview reads health-validation's check catalog, compiled into this
// binary. It never touches a node.
package catalogview

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"sync"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Any matches every value of a filter.
const Any = "ANY"

// PageSize is how many checks one hv_list_checks page holds.
const PageSize = 25

var pinned = sync.OnceValue(func() *catalog.Catalog { return catalog.New() })

// Catalog is the compiled-in catalog with every built-in check registered.
func Catalog() *catalog.Catalog { return pinned() }

// Version is the catalog version compiled in, as a verdict reports it.
func Version() string { return health.AggregateNodeVerdict(nil).CatalogVersion }

// SchemaVersion is the verdict schema the compiled-in catalog writes.
func SchemaVersion() string { return health.NodeVerdictSchemaVersion }

// Cadences are the cadence names a filter accepts.
func Cadences() []string { return []string{"CONTINUOUS", "PROLOG", "MAINTENANCE"} }

// Maturities are the maturity names a filter accepts.
func Maturities() []string { return []string{"stable", "experimental"} }

// Filter selects checks. Each field is a value from its enum, or Any.
type Filter struct {
	Category     string
	Architecture string
	Cadence      string
	Maturity     string
}

// Row is one check in a listing.
type Row struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	Cadence       string   `json:"cadence"`
	Maturity      string   `json:"maturity"`
	Architectures []string `json:"architectures" jsonschema:"empty means every architecture"`
	Contexts      []string `json:"execution_contexts"`
}

// Listing is one page of checks.
type Listing struct {
	CatalogVersion string `json:"catalog_version"`
	Note           string `json:"note"`
	Checks         []Row  `json:"checks"`
	Total          int    `json:"total" jsonschema:"checks matching the filter, across all pages"`
	Page           int    `json:"page"`
	Pages          int    `json:"pages"`
}

const listNote = "An architecture filter also returns checks that apply to every architecture. " +
	"This is the catalog compiled into this server; a node's own config can disable checks, " +
	"and experimental checks are off unless that config turns them on."

// List returns one page of checks matching the filter, sorted by ID.
func List(filter Filter, page int) (*Listing, error) {
	selector, err := filter.selector()
	if err != nil {
		return nil, err
	}

	definitions := Catalog().List(selector)

	pages := max(1, (len(definitions)+PageSize-1)/PageSize)
	if page < 1 {
		page = 1
	}

	listing := &Listing{
		CatalogVersion: Version(), Note: listNote, Checks: []Row{},
		Total: len(definitions), Page: page, Pages: pages,
	}

	start := (page - 1) * PageSize
	for _, definition := range definitions[min(start, len(definitions)):min(start+PageSize, len(definitions))] {
		listing.Checks = append(listing.Checks, Row{
			ID: definition.ID, Name: definition.Name, Category: definition.Category.String(),
			Cadence: definition.SchedulingCadence.String(), Maturity: definition.MaturityLevel.String(),
			Architectures: orEmpty(definition.Architectures), Contexts: contextNames(definition.ExecutionContexts),
		})
	}

	return listing, nil
}

func (f Filter) selector() (catalog.Filter, error) {
	var selector catalog.Filter

	if f.Category != "" && f.Category != Any {
		var category health.Category
		if err := category.UnmarshalJSON([]byte(`"` + f.Category + `"`)); err != nil {
			return selector, fmt.Errorf("unknown category %q", f.Category)
		}

		selector.Category = &category
	}

	if f.Cadence != "" && f.Cadence != Any {
		var cadence health.SchedulingCadence
		if err := cadence.UnmarshalJSON([]byte(`"` + f.Cadence + `"`)); err != nil {
			return selector, fmt.Errorf("unknown cadence %q", f.Cadence)
		}

		selector.Cadence = &cadence
	}

	if f.Maturity != "" && f.Maturity != Any {
		var maturity health.MaturityLevel
		if err := maturity.UnmarshalJSON([]byte(`"` + f.Maturity + `"`)); err != nil {
			return selector, fmt.Errorf("unknown maturity %q", f.Maturity)
		}

		selector.MaturityLevel = &maturity
	}

	if f.Architecture != "" && f.Architecture != Any {
		if !slices.Contains(Architectures, f.Architecture) {
			return selector, fmt.Errorf("unknown architecture %q", f.Architecture)
		}

		selector.Architecture = f.Architecture
	}

	return selector, nil
}

// Threshold is one threshold of a check, as the catalog defines it.
type Threshold struct {
	Default          any            `json:"default,omitempty"`
	Min              any            `json:"min,omitempty"`
	Max              any            `json:"max,omitempty"`
	PerArchitecture  map[string]any `json:"per_architecture,omitempty"`
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	Governance       string         `json:"governance" jsonschema:"FIXED cannot be overridden, DEFAULT can, ADJUSTABLE can within min and max"`
	Unit             string         `json:"unit,omitempty"`
	Description      string         `json:"description,omitempty"`
	RequiredOverride bool           `json:"required_override,omitempty"`
}

// Rule is one rule of a check.
type Rule struct {
	Severity           string   `json:"severity"`
	Expression         string   `json:"expression" jsonschema:"the CEL expression the rule evaluates"`
	Message            string   `json:"message" jsonschema:"the message template; ${name} is filled from signals and thresholds"`
	OperationalState   string   `json:"operational_state,omitempty"`
	RemediationActions []string `json:"remediation_actions,omitempty"`
}

// Description is everything the catalog says about one check.
type Description struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	Description        string      `json:"description"`
	Category           string      `json:"category"`
	Cadence            string      `json:"cadence"`
	Maturity           string      `json:"maturity"`
	OperationalState   string      `json:"operational_state" jsonschema:"the check's default recommendation when a rule names none"`
	CatalogVersion     string      `json:"catalog_version"`
	RemediationActions []string    `json:"remediation_actions"`
	Architectures      []string    `json:"architectures" jsonschema:"empty means every architecture"`
	Contexts           []string    `json:"execution_contexts"`
	RequiredSignals    []string    `json:"required_signals"`
	Dependencies       []string    `json:"dependencies"`
	Rules              []Rule      `json:"rules"`
	Thresholds         []Threshold `json:"thresholds"`
	Truncated          bool        `json:"truncated" jsonschema:"true when rules or thresholds were cut to fit 16 KiB"`
}

// Describe returns one check's definition.
func Describe(id string) (*Description, error) {
	definition, err := Catalog().Get(id)
	if err != nil {
		return nil, fmt.Errorf("unknown check %q: hv_list_checks lists them", id)
	}

	description := &Description{
		ID: definition.ID, Name: definition.Name, Description: definition.Description,
		Category: definition.Category.String(), Cadence: definition.SchedulingCadence.String(),
		Maturity: definition.MaturityLevel.String(), CatalogVersion: Version(),
		OperationalState:   definition.RecommendedAction.OperationalState.String(),
		RemediationActions: actionNames(definition.RecommendedAction.RemediationActions),
		Architectures:      orEmpty(definition.Architectures),
		Contexts:           contextNames(definition.ExecutionContexts),
		RequiredSignals:    orEmpty(definition.RequiredSignals),
		Dependencies:       orEmpty(definition.Dependencies),
		Rules:              []Rule{},
		Thresholds:         []Threshold{},
	}

	for _, rule := range definition.Rules {
		out := Rule{Severity: rule.Severity.String(), Expression: rule.Expression, Message: rule.Message}
		if rule.HasRecommendedAction() {
			out.OperationalState = rule.RecommendedAction.OperationalState.String()
			out.RemediationActions = actionNames(rule.RecommendedAction.RemediationActions)
		}

		description.Rules = append(description.Rules, out)
	}

	for _, name := range slices.Sorted(maps.Keys(definition.Thresholds)) {
		spec := definition.Thresholds[name]
		description.Thresholds = append(description.Thresholds, Threshold{
			Name: name, Type: spec.ValueType.String(), Governance: spec.Category.String(),
			Default: spec.Value, Min: spec.ValueMin, Max: spec.ValueMax, PerArchitecture: spec.PerArchitecture,
			Unit: spec.Unit, Description: spec.Description, RequiredOverride: spec.RequiredOverride,
		})
	}

	fit(description)

	return description, nil
}

// RuleMessage is the catalog's message for the first rule of a check at a severity. It
// is the compiled-in catalog's own text, so it is trusted, but its ${} fields are unfilled.
func RuleMessage(id, severity string) string {
	definition, err := Catalog().Get(id)
	if err != nil {
		return ""
	}

	for _, rule := range definition.Rules {
		if rule.Severity.String() == severity {
			return rule.Message
		}
	}

	return ""
}

// CheckName is the catalog name of a check, or "" when the compiled-in catalog lacks it.
func CheckName(id string) string {
	definition, err := Catalog().Get(id)
	if err != nil {
		return ""
	}

	return definition.Name
}

// CheckIDs lists every registered check, sorted.
func CheckIDs() []string {
	definitions := Catalog().List(catalog.Filter{})

	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}

	return ids
}

// LiveArchitectures is every architecture a registered check names, sorted.
func LiveArchitectures() []string {
	seen := map[string]bool{}
	for _, definition := range Catalog().List(catalog.Filter{}) {
		for _, architecture := range definition.Architectures {
			seen[architecture] = true
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// LiveCategories is every category a registered check uses, sorted by name.
func LiveCategories() []string {
	var names []string
	for _, category := range Catalog().Categories() {
		names = append(names, category.String())
	}

	sort.Strings(names)

	return names
}

func contextNames(contexts []health.ExecutionContext) []string {
	names := make([]string, 0, len(contexts))
	for _, context := range contexts {
		names = append(names, context.String())
	}

	return names
}

func actionNames(actions []health.RemediationAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, string(action))
	}

	return names
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}

	return slices.Clone(values)
}

// serveCheckIDs are verdicts serve builds itself, outside the catalog.
var serveCheckIDs = []string{"architecture_discovery", "collection_source_busy"}

// xidCheckID matches the per-code Xid checks, whose codes the catalog may not list yet.
var xidCheckID = regexp.MustCompile(`^gpu_xid_(?:[0-9]{1,5}|149_[0-9a-f]{1,8})$`)

var signals = sync.OnceValue(func() map[string]bool {
	known := map[string]bool{"source": true} // collection_source_busy's observed value
	for _, definition := range Catalog().List(catalog.Filter{}) {
		for _, signal := range definition.RequiredSignals {
			known[signal] = true
		}
	}

	return known
})

// KnownCheckID says whether id is a check the compiled-in catalog or serve itself names,
// or an Xid check by its shape.
func KnownCheckID(id string) bool {
	return CheckName(id) != "" || slices.Contains(serveCheckIDs, id) || xidCheckID.MatchString(id)
}

// KnownSignal says whether name is a signal some check in the compiled-in catalog reads.
// Observed values are keyed by these.
func KnownSignal(name string) bool { return signals()[name] }
