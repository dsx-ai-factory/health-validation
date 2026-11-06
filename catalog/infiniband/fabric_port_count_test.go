// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

const fabricCountCheck = "rdma_fabric_port_count"

func b200Catalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("B200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

// nic builds one healthy single-port device at the given rate.
func nic(device, layer string, rate float64) infiniband.PortMeasurement {
	return infiniband.PortMeasurement{
		Device:        device,
		Port:          1,
		State:         "ACTIVE",
		PhysicalState: "LinkUp",
		RateGbps:      rate,
		LinkLayer:     layer,
	}
}

// downNIC is a port left administratively down: sysfs still reports its
// configured rate.
func downNIC(device, layer string, rate float64) infiniband.PortMeasurement {
	m := nic(device, layer, rate)
	m.State, m.PhysicalState = "DOWN", "Disabled"
	return m
}

// hgxB200 is an HGX B200 node as observed in the field: eight 400 Gb/s RoCE
// rails, two 200 Gb/s front-end ports and four 100 Gb/s InfiniBand storage
// ports. Ports named in replace take the place of the one with their device.
func hgxB200(replace ...infiniband.PortMeasurement) []infiniband.PortMeasurement {
	var all []infiniband.PortMeasurement
	for i := range 8 {
		all = append(all, nic(fmt.Sprintf("mlx5_%d", i), "Ethernet", 400))
	}
	all = append(all, nic("mlx5_8", "Ethernet", 200), nic("mlx5_9", "Ethernet", 200))
	for i := range 4 {
		all = append(all, nic(fmt.Sprintf("mlx5_%d", 10+i), "InfiniBand", 100))
	}
	for _, r := range replace {
		for i := range all {
			if all[i].Device == r.Device {
				all[i] = r
			}
		}
	}
	return all
}

func without(ports []infiniband.PortMeasurement, device string) []infiniband.PortMeasurement {
	return slices.DeleteFunc(slices.Clone(ports), func(p infiniband.PortMeasurement) bool { return p.Device == device })
}

func countOverride(name string, value any) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID: fabricCountCheck, ThresholdName: name, Value: value,
	}))
}

func TestRDMAFabricPortCountDefinition(t *testing.T) {
	def := infiniband.RDMAFabricPortCount()
	if def.ID != fabricCountCheck {
		t.Errorf("ID = %q", def.ID)
	}
	if def.Category != health.InfiniBand {
		t.Errorf("Category = %v, want InfiniBand", def.Category)
	}
	if got, want := def.RequiredSignals, []string{infiniband.SignalPortRatesGbps}; !slices.Equal(got, want) {
		t.Errorf("RequiredSignals = %v, want %v", got, want)
	}
	if got := def.Architectures; !slices.Equal(got, []string{"B200"}) {
		t.Errorf("Architectures = %v, want [B200]", got)
	}
	if def.ConditionScope != health.NodeScope {
		t.Errorf("ConditionScope = %v, want NodeScope", def.ConditionScope)
	}
	for _, name := range []string{"expected_fabric_ports", "fabric_port_rate_gbps"} {
		spec, ok := def.Thresholds[name]
		if !ok {
			t.Errorf("missing threshold %q", name)
			continue
		}
		if spec.Category != health.DefaultThreshold {
			t.Errorf("%s Category = %v, want DefaultThreshold", name, spec.Category)
		}
	}
	if len(def.Rules) != 1 || def.Rules[0].Severity != health.Critical {
		t.Fatalf("Rules = %v, want one CRITICAL rule", def.Rules)
	}
}

func TestRDMAFabricPortCount(t *testing.T) {
	tests := []struct {
		name           string
		cat            func() *catalog.Catalog
		measurement    infiniband.PortInventoryMeasurement
		wantSeverity   health.Severity
		wantSkipReason health.SkipReason
		wantSkipped    bool
	}{
		{
			name:         "all eight rails present; front-end and storage ports ignored",
			measurement:  infiniband.InventoryOf(hgxB200()),
			wantSeverity: health.Pass,
		},
		{
			// Presence, not state: down rails still enumerate at their
			// configured rate, and ib_port_state reports them by name.
			name: "three rails administratively down still count",
			measurement: infiniband.InventoryOf(hgxB200(
				downNIC("mlx5_2", "Ethernet", 400), downNIC("mlx5_5", "Ethernet", 400), downNIC("mlx5_7", "Ethernet", 400),
			)),
			wantSeverity: health.Pass,
		},
		{
			name:         "a rail missing from sysfs",
			measurement:  infiniband.InventoryOf(without(hgxB200(), "mlx5_6")),
			wantSeverity: health.Critical,
		},
		{
			// No longer a fabric port, so the count falls short.
			name:         "a rail trained down to 200 Gb/s",
			measurement:  infiniband.InventoryOf(hgxB200(nic("mlx5_4", "Ethernet", 200))),
			wantSeverity: health.Critical,
		},
		{
			name: "more fabric ports than expected",
			measurement: infiniband.InventoryOf(append(hgxB200(),
				nic("mlx5_14", "Ethernet", 400), nic("mlx5_15", "Ethernet", 400))),
			wantSeverity: health.Pass,
		},
		{
			name:         "InfiniBand rails count the same as RoCE",
			measurement:  infiniband.InventoryOf(hgxB200(nic("mlx5_0", "InfiniBand", 400))),
			wantSeverity: health.Pass,
		},
		{
			name:         "no ports at all",
			measurement:  infiniband.InventoryOf(nil),
			wantSeverity: health.Critical,
		},
		{
			name:         "override tolerates a node built with six rails",
			cat:          func() *catalog.Catalog { return b200Catalog(countOverride("expected_fabric_ports", int64(6))) },
			measurement:  infiniband.InventoryOf(without(without(hgxB200(), "mlx5_6"), "mlx5_7")),
			wantSeverity: health.Pass,
		},
		{
			name:         "override counts 200 Gb/s ports as fabric",
			cat:          func() *catalog.Catalog { return b200Catalog(countOverride("fabric_port_rate_gbps", float64(200))) },
			measurement:  infiniband.InventoryOf(hgxB200(nic("mlx5_4", "Ethernet", 200))),
			wantSeverity: health.Pass,
		},
		{
			name:         "collection failure reports UNKNOWN",
			measurement:  infiniband.ErroredPortInventory(errors.New("sysfs unreadable")),
			wantSeverity: health.Unknown,
		},
		{
			name: "skipped on an unlisted architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(catalog.WithArchitecture("A100"), catalog.WithExecutionContext(health.OperatorInfra))
			},
			measurement:    infiniband.InventoryOf(hgxB200()),
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat := b200Catalog()
			if tc.cat != nil {
				cat = tc.cat()
			}
			v, err := cat.Evaluate(fabricCountCheck, tc.measurement)
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped != tc.wantSkipped {
				t.Fatalf("Skipped = %v, want %v (reason %v)", v.Skipped, tc.wantSkipped, v.SkipReason)
			}
			if tc.wantSkipped {
				if v.SkipReason != tc.wantSkipReason {
					t.Errorf("SkipReason = %v, want %v", v.SkipReason, tc.wantSkipReason)
				}
				return
			}
			if v.Severity != tc.wantSeverity {
				t.Errorf("Severity = %v, want %v (messages %v)", v.Severity, tc.wantSeverity, v.Messages)
			}
		})
	}
}
