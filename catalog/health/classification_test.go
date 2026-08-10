package health

import (
	"encoding/json"
	"testing"
)

func TestCategoryContract(t *testing.T) {
	tests := []struct {
		name     string
		jsonName string
		category Category
		ordinal  int
	}{
		{name: "GPU", jsonName: "gpu", category: GPU, ordinal: 0},
		{name: "NVLink", jsonName: "nvlink", category: NVLink, ordinal: 1},
		{name: "PCIe", jsonName: "pcie", category: PCIe, ordinal: 2},
		{name: "InfiniBand", jsonName: "infiniband", category: InfiniBand, ordinal: 3},
		{name: "Storage", jsonName: "storage", category: Storage, ordinal: 4},
		{name: "Node", jsonName: "node", category: Node, ordinal: 5},
		{name: "Xid", jsonName: "xid", category: Xid, ordinal: 6},
		{name: "CPU", jsonName: "cpu", category: CPU, ordinal: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int(tt.category); got != tt.ordinal {
				t.Fatalf("ordinal = %d, want %d", got, tt.ordinal)
			}
			if got := tt.category.String(); got != tt.jsonName {
				t.Errorf("String = %q, want %q", got, tt.jsonName)
			}

			data, err := json.Marshal(tt.category)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got, want := string(data), `"`+tt.jsonName+`"`; got != want {
				t.Errorf("Marshal = %s, want %s", got, want)
			}

			var roundTrip Category
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if roundTrip != tt.category {
				t.Errorf("round trip = %v, want %v", roundTrip, tt.category)
			}
		})
	}
}

func TestCategoryMapKeyJSON(t *testing.T) {
	data, err := json.Marshal(map[Category]Severity{
		GPU:  Warning,
		Node: Pass,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var fields map[string]Severity
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Unmarshal string-keyed map: %v", err)
	}
	if got := fields["gpu"]; got != Warning {
		t.Errorf("gpu severity = %v, want WARNING", got)
	}
	if got := fields["node"]; got != Pass {
		t.Errorf("node severity = %v, want PASS", got)
	}
	if _, ok := fields["0"]; ok {
		t.Errorf("numeric category key present in JSON: %s", data)
	}

	var roundTrip map[Category]Severity
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal category-keyed map: %v", err)
	}
	if got := roundTrip[GPU]; got != Warning {
		t.Errorf("round-trip gpu severity = %v, want WARNING", got)
	}
	if got := roundTrip[Node]; got != Pass {
		t.Errorf("round-trip node severity = %v, want PASS", got)
	}
}

func TestCategoryMapKeyJSON_RejectsNumericKey(t *testing.T) {
	var got map[Category]Severity
	if err := json.Unmarshal([]byte(`{"0":"WARNING"}`), &got); err == nil {
		t.Fatal("Unmarshal numeric category key succeeded, want error")
	}
}

func TestMaturityLevelContract(t *testing.T) {
	tests := []struct {
		name     string
		jsonName string
		maturity MaturityLevel
		ordinal  int
	}{
		{name: "Stable", jsonName: "stable", maturity: Stable, ordinal: 0},
		{name: "Experimental", jsonName: "experimental", maturity: Experimental, ordinal: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int(tt.maturity); got != tt.ordinal {
				t.Fatalf("ordinal = %d, want %d", got, tt.ordinal)
			}
			if got := tt.maturity.String(); got != tt.jsonName {
				t.Errorf("String = %q, want %q", got, tt.jsonName)
			}

			data, err := json.Marshal(tt.maturity)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got, want := string(data), `"`+tt.jsonName+`"`; got != want {
				t.Errorf("Marshal = %s, want %s", got, want)
			}

			var roundTrip MaturityLevel
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if roundTrip != tt.maturity {
				t.Errorf("round trip = %v, want %v", roundTrip, tt.maturity)
			}
		})
	}
}

func TestMaturityLevel_UnmarshalInvalid(t *testing.T) {
	var ml MaturityLevel
	if err := json.Unmarshal([]byte(`"BOGUS"`), &ml); err == nil {
		t.Error("Unmarshal of unknown maturity level: want error, got nil")
	}
}
