/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package catalog

import (
	"testing"

	"github.com/NVIDIA/health-validation/catalog/health"
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
	cat.MustRegister(health.CheckDefinition{ID: "gpu_thermal", Category: health.GPU, MaturityLevel: "stable"})
	cat.MustRegister(health.CheckDefinition{ID: "nvlink_replay", Category: health.NVLink, MaturityLevel: "stable"})
	cat.MustRegister(health.CheckDefinition{ID: "gpu_xid", Category: health.GPU, MaturityLevel: "experimental"})

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
		got := cat.List(Filter{MaturityLevel: "stable"})
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
