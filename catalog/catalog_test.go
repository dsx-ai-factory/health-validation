package catalog

import (
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestCatalog_RegisterAndGet(t *testing.T) {
	cat := NewEmpty()
	cd := health.CheckDefinition{ID: "test_check", Name: "Test"}

	if err := cat.Register(cd); err != nil {
		t.Fatal(err)
	}

	got, err := cat.Get("test_check")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test" {
		t.Errorf("name = %q, want %q", got.Name, "Test")
	}
}

func TestCatalog_RegisterDuplicate(t *testing.T) {
	cat := NewEmpty()
	cd := health.CheckDefinition{ID: "dup"}

	if err := cat.Register(cd); err != nil {
		t.Fatal(err)
	}
	if err := cat.Register(cd); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestCatalog_GetNotFound(t *testing.T) {
	cat := NewEmpty()
	if _, err := cat.Get("nope"); err == nil {
		t.Error("expected error for missing check")
	}
}

func TestCatalog_MustRegisterPanics(t *testing.T) {
	cat := NewEmpty()
	cat.MustRegister(health.CheckDefinition{ID: "x"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate MustRegister")
		}
	}()
	cat.MustRegister(health.CheckDefinition{ID: "x"})
}

func TestCatalog_List(t *testing.T) {
	cat := NewEmpty()
	cat.MustRegister(health.CheckDefinition{ID: "gpu_thermal", Category: health.GPU, MaturityLevel: health.Stable})
	cat.MustRegister(health.CheckDefinition{ID: "nvlink_replay", Category: health.NVLink, MaturityLevel: health.Stable})
	cat.MustRegister(health.CheckDefinition{ID: "gpu_xid", Category: health.GPU, MaturityLevel: health.Experimental})

	t.Run("all", func(t *testing.T) {
		got := cat.List(Filter{})
		if len(got) != 3 {
			t.Errorf("got %d, want 3", len(got))
		}
	})

	t.Run("by_category", func(t *testing.T) {
		gpu := health.GPU
		got := cat.List(Filter{Category: &gpu})
		if len(got) != 2 {
			t.Errorf("got %d, want 2", len(got))
		}
	})

	t.Run("by_maturity", func(t *testing.T) {
		stable := health.Stable
		got := cat.List(Filter{MaturityLevel: &stable})
		if len(got) != 2 {
			t.Errorf("got %d, want 2", len(got))
		}
	})

	t.Run("sorted_by_id", func(t *testing.T) {
		got := cat.List(Filter{})
		for i := 1; i < len(got); i++ {
			if got[i-1].ID >= got[i].ID {
				t.Errorf("not sorted: %q >= %q", got[i-1].ID, got[i].ID)
			}
		}
	})
}

func TestCatalog_ListByArchitecture(t *testing.T) {
	cat := NewEmpty()
	cat.MustRegister(health.CheckDefinition{ID: "gb200_only", Architectures: []string{"GB200"}})
	cat.MustRegister(health.CheckDefinition{ID: "h100_only", Architectures: []string{"H100"}})
	cat.MustRegister(health.CheckDefinition{ID: "all_arch"})

	got := cat.List(Filter{Architecture: "GB200"})
	if len(got) != 2 {
		t.Errorf("got %d, want 2 (gb200_only + all_arch)", len(got))
	}
}

func TestCatalog_Len(t *testing.T) {
	cat := NewEmpty()
	if cat.Len() != 0 {
		t.Errorf("empty catalog Len() = %d", cat.Len())
	}
	cat.MustRegister(health.CheckDefinition{ID: "a"})
	cat.MustRegister(health.CheckDefinition{ID: "b"})
	if cat.Len() != 2 {
		t.Errorf("Len() = %d, want 2", cat.Len())
	}
}

// A fixed threshold that also requires an override describes a check that can
// never run: unset it skips, set it is rejected. Registration must reject it.
func TestCatalog_RegisterRejectsRequiredFixedThreshold(t *testing.T) {
	err := NewEmpty().Register(health.CheckDefinition{
		ID:       "contradictory_threshold_check",
		Category: health.Node,
		Thresholds: map[string]health.ThresholdSpec{
			"expected_value": {
				Value:            int64(0),
				Category:         health.FixedThreshold,
				RequiredOverride: true,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot require an override") {
		t.Fatalf("Register error = %v, want fixed-and-required rejection", err)
	}
}

func TestCatalog_RegisterRejectsIncompletePerArchitecture(t *testing.T) {
	err := NewEmpty().Register(health.CheckDefinition{
		ID:            "incomplete_per_arch",
		Architectures: []string{"GB200", "GB300"},
		Thresholds: map[string]health.ThresholdSpec{
			"expected_value": {
				PerArchitecture: map[string]any{"GB200": int64(1)},
				Category:        health.DefaultThreshold,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "GB300") {
		t.Fatalf("Register error = %v, want missing GB300 named", err)
	}
}
