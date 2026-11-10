// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/google/cel-go/cel"
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

func TestNew_RegistersBuiltinCheck(t *testing.T) {
	ids := []string{
		"nvlink_active",
		"gpu_power_state",
		"gpu_power_limit",
		"gpu_average_power_draw",
		"gpu_compute_apps",
		"cpu_count",
		"numa_node_count",
		"nvlink_capabilities",
		"nvlink_fabric",
	}

	cat := catalog.New()
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if _, err := cat.Get(id); err != nil {
				t.Errorf("Get(%s) = %v, want check to ship in builtinChecks()", id, err)
			}
		})
	}
}

// A built-in check is node-scoped unless its own signals describe state outside
// this node. Registering a non-NODE check means listing it here, so a scope
// change is always deliberate. What this cannot catch is a new check that
// should have been DOMAIN and was left at the NODE default; that is a judgment
// call, not a fact.
func TestBuiltinChecks_ConditionScope(t *testing.T) {
	nonNode := map[string]health.ConditionScope{
		"nvlink_fabric_health": health.DomainScope,
		"nvlink_fabric":        health.DomainScope,
		"rdma_linkflap_fatal":  health.ClusterScope,
		"ib_port_state":        health.ClusterScope,
	}

	seen := make(map[string]bool)
	for _, cd := range catalog.New().List(catalog.Filter{}) {
		seen[cd.ID] = true
		want, ok := nonNode[cd.ID]
		if !ok {
			want = health.NodeScope
		}
		if cd.ConditionScope != want {
			t.Errorf("%s: ConditionScope = %v, want %v", cd.ID, cd.ConditionScope, want)
		}
	}
	for id := range nonNode {
		if !seen[id] {
			t.Errorf("%q is listed as non-NODE but is not registered", id)
		}
	}
}

// healthdump marshals check definitions for the docs catalog, so the definition
// tag is its own contract, separate from the one CheckVerdict carries.
// gpu_count is NodeScope, the zero value an omitempty tag would silently drop;
// nvlink_fabric proves a non-zero value is not merely reading as a default.
func TestBuiltinChecks_ConditionScopeSerializes(t *testing.T) {
	for id, want := range map[string]string{"gpu_count": "NODE", "nvlink_fabric": "DOMAIN"} {
		cd, err := catalog.New().Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		data, err := json.Marshal(cd)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", id, err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", id, err)
		}
		if got["condition_scope"] != want {
			t.Errorf("%s: condition_scope = %v, want %q; json = %s", id, got["condition_scope"], want, data)
		}
	}
}

// Collection requests only a runnable check's RequiredSignals, so a rule that
// read an undeclared signal would see that field's zero value.
func TestBuiltinRulesReadOnlyDeclaredInputs(t *testing.T) {
	for _, cd := range catalog.New().List(catalog.Filter{}) {
		names := make(map[string]bool)
		for _, s := range cd.RequiredSignals {
			names[s] = true
		}
		for name := range cd.Thresholds {
			names[name] = true
		}
		var opts []cel.EnvOption
		for name := range names {
			opts = append(opts, cel.Variable(name, cel.DynType))
		}
		env, err := cel.NewEnv(opts...)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range cd.Rules {
			if _, iss := env.Compile(rule.Expression); iss.Err() != nil {
				t.Errorf("%s: rule %q reads an input outside RequiredSignals and Thresholds: %v", cd.ID, rule.Expression, iss.Err())
			}
		}
	}
}

// Every check reading the shared NVML per-device enumeration depends on
// gpu_count, so a verdict about a device missing from that enumeration names
// gpu_count as the cause rather than reading as an independent fault. The
// cohort matches the sensors sharing the NVML backend resource key in toolkit:
// GPU, Node, and NVLink.
func TestBuiltinChecks_NVMLCohortDependsOnGPUCount(t *testing.T) {
	cohort := []string{
		"gpu_average_power_draw",
		"gpu_power_limit",
		"gpu_power_state",
		"gpu_sensor_throttle",
		"nvlink_active",
		"nvlink_bandwidth_count_speed",
		"nvlink_capabilities",
		"nvlink_fabric",
		"nvlink_fabric_health",
		"nvlink_p2p",
	}

	cat := catalog.New(catalog.WithArchitecture("GB200"))
	for _, id := range cohort {
		cd, err := cat.Get(id)
		if err != nil {
			t.Errorf("Get(%q): %v", id, err)
			continue
		}
		if !slices.Contains(cd.Dependencies, "gpu_count") {
			t.Errorf("%s dependencies = %v, want to contain gpu_count", id, cd.Dependencies)
		}
	}

	// gpu_count is the root of the cohort and depends on nothing itself.
	root, err := cat.Get("gpu_count")
	if err != nil {
		t.Fatalf("Get(gpu_count): %v", err)
	}
	if len(root.Dependencies) != 0 {
		t.Errorf("gpu_count dependencies = %v, want none", root.Dependencies)
	}
}
