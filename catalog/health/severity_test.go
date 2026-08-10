package health

import (
	"encoding/json"
	"testing"
)

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		want string
		sev  Severity
	}{
		{want: "PASS", sev: Pass},
		{want: "INFO", sev: Info},
		{want: "WARNING", sev: Warning},
		{want: "CRITICAL", sev: Critical},
		{want: "UNKNOWN", sev: Unknown},
	}
	for _, tt := range tests {
		if got := tt.sev.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestSeverity_JSONRoundTrip(t *testing.T) {
	for _, sev := range []Severity{Pass, Info, Warning, Critical, Unknown} {
		data, err := json.Marshal(sev)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", sev, err)
		}

		var got Severity
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if got != sev {
			t.Errorf("round-trip: got %v, want %v", got, sev)
		}
	}
}

func TestSeverity_UnmarshalInvalid(t *testing.T) {
	var s Severity
	if err := json.Unmarshal([]byte(`"BOGUS"`), &s); err == nil {
		t.Error("expected error for unknown severity")
	}
}

// TestSeverity_Ordering pins the numeric ordering of Severity
// constants. Worse() and several aggregation paths (NodeVerdict
// roll-up, multi-rule evaluation) compare severities with `>`, so the
// non-Unknown levels must form a total order Pass < Info < Warning <
// Critical. Unknown sits above Critical so a check that couldn't
// complete is treated as worse than any concrete result during
// aggregation. If you reorder these constants, audit Worse(),
// NodeVerdict aggregation, and the evaluator's rule-priority logic.
func TestSeverity_Ordering(t *testing.T) {
	want := []Severity{Pass, Info, Warning, Critical, Unknown}
	for i := 1; i < len(want); i++ {
		if want[i-1] >= want[i] {
			t.Errorf("severity %v (%d) should be less than %v (%d)",
				want[i-1], int(want[i-1]), want[i], int(want[i]))
		}
	}
}

func TestWorse(t *testing.T) {
	tests := []struct {
		a, b Severity
		want Severity
	}{
		{Pass, Pass, Pass},
		{Pass, Warning, Warning},
		{Critical, Warning, Critical},
		{Unknown, Critical, Unknown},
		{Info, Pass, Info},
	}
	for _, tt := range tests {
		if got := Worse(tt.a, tt.b); got != tt.want {
			t.Errorf("Worse(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
