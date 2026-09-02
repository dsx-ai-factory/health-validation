package health

import (
	_ "embed"
	"encoding/json"
	"slices"
	"testing"
)

//go:embed node-verdict.schema.json
var nodeVerdictSchema []byte

// The checked-in JSON Schema for NodeVerdict must track the Go contract:
// the pinned schema_version and every enum's set of wire values. This fails
// when the version is bumped, or an enum value is added / renamed / removed,
// without the schema being updated to match — the two drift silently
// otherwise, since nothing else couples them.
func TestNodeVerdictSchema_InSyncWithContract(t *testing.T) {
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
		Properties struct {
			SchemaVersion struct {
				Const string `json:"const"`
			} `json:"schema_version"`
			CatalogVersion json.RawMessage `json:"catalog_version"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(nodeVerdictSchema, &doc); err != nil {
		t.Fatalf("parse node-verdict.schema.json: %v", err)
	}

	if doc.Properties.SchemaVersion.Const != NodeVerdictSchemaVersion {
		t.Errorf("schema_version const = %q, want %q (NodeVerdictSchemaVersion); update node-verdict.schema.json",
			doc.Properties.SchemaVersion.Const, NodeVerdictSchemaVersion)
	}
	if len(doc.Properties.CatalogVersion) == 0 {
		t.Error("catalog_version missing from schema properties")
	}
	if !slices.Contains(doc.Required, "catalog_version") {
		t.Error("catalog_version missing from schema required fields")
	}

	// Each enum's wire values are defined once, in the Go name maps; the
	// schema's $defs must carry exactly the same set.
	enums := []struct {
		def    string
		values []string
	}{
		{"severity", wireValues(severityNames)},
		{"category", wireValues(categoryNames)},
		{"conditionScope", wireValues(conditionScopeNames)},
		{"skipReason", wireValues(skipReasonNames)},
		{"maturityLevel", wireValues(maturityLevelNames)},
		{"thresholdCategory", wireValues(thresholdCategoryNames)},
		{"operationalState", wireValues(operationalStateNames)},
	}
	for _, e := range enums {
		want := slices.Clone(e.values)
		slices.Sort(want)
		got := slices.Clone(doc.Defs[e.def].Enum)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("$defs.%s.enum drift: schema has %v, code defines %v; update node-verdict.schema.json",
				e.def, got, want)
		}
	}
}

// wireValues returns the JSON wire strings of an int-backed enum's name map.
func wireValues[K comparable](m map[K]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// The enum table above pins conditionScope's wire values but not that anything
// uses them, so the property can vanish from checkVerdict while the $def stays
// and every other test passes.
func TestNodeVerdictSchema_CheckVerdictDeclaresConditionScope(t *testing.T) {
	var doc struct {
		Defs struct {
			CheckVerdict struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"checkVerdict"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(nodeVerdictSchema, &doc); err != nil {
		t.Fatalf("parse node-verdict.schema.json: %v", err)
	}
	if _, ok := doc.Defs.CheckVerdict.Properties["condition_scope"]; !ok {
		t.Error("$defs.checkVerdict.properties.condition_scope missing; the schema must describe every field CheckVerdict emits")
	}
}
