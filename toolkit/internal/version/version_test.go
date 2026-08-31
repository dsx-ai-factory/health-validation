package version

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	// In an unstamped test build the release version stays "dev"; commit and
	// date may be backfilled from Go's VCS build info, so we don't assert on
	// them.
	got := Get()
	if got.Version != "dev" {
		t.Errorf("Version = %q, want %q (unstamped build)", got.Version, "dev")
	}
	if got.CatalogVersion == "" {
		t.Error("CatalogVersion is empty")
	}
	if got.GoVersion == "" {
		t.Error("GoVersion is empty; want the runtime Go version")
	}
}

func TestStringContainsFields(t *testing.T) {
	info := Info{
		Version:        "v1.4.0",
		CatalogVersion: "v1.2.0",
		Commit:         "a1b2c3d4e5f6789000",
		Date:           "2026-06-02T12:00:00Z",
		GoVersion:      "go1.25.1",
	}
	s := info.String()
	// Commit is shortened to 12 chars in the human line.
	for _, want := range []string{"health-validation", "v1.4.0", "a1b2c3d4e5f6", "2026-06-02T12:00:00Z", "go1.25.1"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
	if strings.Contains(s, "789000") {
		t.Errorf("String() = %q, commit should be truncated to %d chars", s, shortCommitLen)
	}
}

func TestStringPreservesDirty(t *testing.T) {
	// A 40-char SHA plus the "+dirty" suffix is well over shortCommitLen; the
	// suffix must survive truncation (it's the whole point of the marker).
	info := Info{
		Version:        "dev",
		CatalogVersion: "dev",
		Commit:         "a1b2c3d4e5f6789000aaaabbbbccccddddeeeeff+dirty",
		Date:           "unknown",
		GoVersion:      "go1.25.1",
	}
	s := info.String()
	if !strings.Contains(s, "a1b2c3d4e5f6") {
		t.Errorf("String() = %q, want the shortened commit", s)
	}
	if !strings.Contains(s, "+dirty") {
		t.Errorf("String() = %q, want the +dirty marker preserved", s)
	}
	if strings.Contains(s, "eeeeff") {
		t.Errorf("String() = %q, commit body should be truncated", s)
	}
}

func TestInfoJSONRoundTrip(t *testing.T) {
	want := Info{Version: "v1.4.0", CatalogVersion: "v1.2.0", Commit: "abc", Date: "d", GoVersion: "go1.25.1"}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Info
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	// Spot-check the lowerCamel JSON keys consumers will depend on.
	for _, key := range []string{`"catalogVersion"`, `"goVersion"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON = %s, want a %s key", b, key)
		}
	}
}
