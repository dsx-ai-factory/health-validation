// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"slices"
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

// Categories reports each registered check's category once, sorted, so a
// whole-node run can declare exactly the domains the catalog can assess.
func TestCatalog_Categories(t *testing.T) {
	cat := NewEmpty()
	if got := cat.Categories(); len(got) != 0 {
		t.Errorf("empty catalog Categories() = %v, want none", got)
	}

	cat.MustRegister(health.CheckDefinition{ID: "n1", Category: health.Node})
	cat.MustRegister(health.CheckDefinition{ID: "n2", Category: health.Node})
	cat.MustRegister(health.CheckDefinition{ID: "g1", Category: health.GPU})

	got := cat.Categories()
	want := []health.Category{health.GPU, health.Node}
	if !slices.Equal(got, want) {
		t.Errorf("Categories() = %v, want %v", got, want)
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
				ValueType:        health.ThresholdInt64,
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
				ValueType:       health.ThresholdInt64,
				Category:        health.DefaultThreshold,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "GB300") {
		t.Fatalf("Register error = %v, want missing GB300 named", err)
	}
}

// Normalization is what lets authors write bare literals: the stored
// definition must carry the declared type, since CEL is handed these
// values directly.
func TestCatalog_RegisterNormalizesThresholdValues(t *testing.T) {
	cat := NewEmpty()
	if err := cat.Register(health.CheckDefinition{
		ID: "normalize_me",
		Thresholds: map[string]health.ThresholdSpec{
			"expected_count": {
				Value:           4,
				PerArchitecture: map[string]any{"GB200": 8},
				ValueType:       health.ThresholdInt64,
				ValueMin:        1,
				Category:        health.DefaultThreshold,
			},
			"min_power_w": {
				Value:     65,
				ValueType: health.ThresholdFloat64,
				Category:  health.DefaultThreshold,
			},
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cd, err := cat.Get("normalize_me")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	count := cd.Thresholds["expected_count"]
	if count.Value != int64(4) {
		t.Errorf("Value = %#v, want int64(4)", count.Value)
	}
	if count.PerArchitecture["GB200"] != int64(8) {
		t.Errorf("PerArchitecture[GB200] = %#v, want int64(8)", count.PerArchitecture["GB200"])
	}
	if count.ValueMin != int64(1) {
		t.Errorf("ValueMin = %#v, want int64(1)", count.ValueMin)
	}
	if power := cd.Thresholds["min_power_w"]; power.Value != float64(65) {
		t.Errorf("min_power_w = %#v, want float64(65)", power.Value)
	}
}

// A struct copy shares its maps with the caller, so normalization has to
// rebuild them rather than rewrite the definition the caller still holds.
func TestCatalog_RegisterDoesNotMutateCallerThresholds(t *testing.T) {
	perArch := map[string]any{"GB200": 8}
	cd := health.CheckDefinition{
		ID: "no_mutate",
		Thresholds: map[string]health.ThresholdSpec{
			"expected_count": {
				Value:           4,
				PerArchitecture: perArch,
				ValueType:       health.ThresholdInt64,
				Category:        health.DefaultThreshold,
			},
		},
	}

	if err := NewEmpty().Register(cd); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if got := cd.Thresholds["expected_count"].Value; got != 4 {
		t.Errorf("caller Value = %#v, want the original int 4", got)
	}
	if got := perArch["GB200"]; got != 8 {
		t.Errorf("caller PerArchitecture[GB200] = %#v, want the original int 8", got)
	}
}

func TestCatalog_RegisterRejectsUndeclaredThresholdType(t *testing.T) {
	err := NewEmpty().Register(health.CheckDefinition{
		ID: "untyped_threshold",
		Thresholds: map[string]health.ThresholdSpec{
			"expected_count": {Value: int64(4), Category: health.DefaultThreshold},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "ValueType is required") {
		t.Fatalf("Register error = %v, want an undeclared-type rejection", err)
	}
}
