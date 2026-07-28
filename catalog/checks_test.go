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

package catalog_test

import (
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
)

func TestNew_HasBuiltinChecks(t *testing.T) {
	cat := catalog.New()
	if cat.Len() == 0 {
		t.Fatal("New() created catalog with no checks")
	}
}

func TestNew_UniqueIDs(t *testing.T) {
	cat := catalog.New()
	checks := cat.List(catalog.Filter{})
	seen := make(map[string]bool)
	for _, cd := range checks {
		if cd.ID == "" {
			t.Error("check has empty ID")
		}
		if seen[cd.ID] {
			t.Errorf("duplicate check ID %q", cd.ID)
		}
		seen[cd.ID] = true
	}
}

func TestNew_FilterByCategory(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	xid := health.Xid
	xidChecks := cat.List(catalog.Filter{Category: &xid})
	if len(xidChecks) == 0 {
		t.Error("expected Xid checks in auto-loaded catalog")
	}
}

func TestNew_RegistersNVLinkActive(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_active"); err != nil {
		t.Errorf("Get(nvlink_active) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersGPUPowerState(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("gpu_power_state"); err != nil {
		t.Errorf("Get(gpu_power_state) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersNVLinkCapabilities(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_capabilities"); err != nil {
		t.Errorf("Get(nvlink_capabilities) = %v, want check to ship in builtinChecks()", err)
	}
}

func TestNew_RegistersNVLinkFabric(t *testing.T) {
	cat := catalog.New()
	if _, err := cat.Get("nvlink_fabric"); err != nil {
		t.Errorf("Get(nvlink_fabric) = %v, want check to ship in builtinChecks()", err)
	}
}
