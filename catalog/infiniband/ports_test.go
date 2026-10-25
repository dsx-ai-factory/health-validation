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

const fabricPortsCheck = "rdma_fabric_ports_active"

func portsCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("B200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

type port struct {
	key       string
	layer     string
	state     int64
	physState int64
	rate      float64
}

func active(key string, rate float64) port {
	return port{key: key, layer: "Ethernet", state: infiniband.PortStateActive, physState: infiniband.PortPhysicalStateLinkUp, rate: rate}
}

// down is a port left administratively down: sysfs still reports its
// configured rate.
func down(key string, rate float64) port {
	return port{key: key, layer: "Ethernet", state: 1, physState: 3, rate: rate}
}

func ports(ps ...port) infiniband.PortMeasurement {
	m := infiniband.PortMeasurement{
		States:         map[string]int64{},
		PhysicalStates: map[string]int64{},
		RatesGbps:      map[string]float64{},
		LinkLayers:     map[string]string{},
	}
	for _, p := range ps {
		m.States[p.key] = p.state
		m.PhysicalStates[p.key] = p.physState
		m.RatesGbps[p.key] = p.rate
		m.LinkLayers[p.key] = p.layer
	}
	return m
}

// rails returns n active 400 Gb/s fabric ports named mlx5_<first>..
func rails(first, n int) []port {
	var out []port
	for i := range n {
		out = append(out, active(fmt.Sprintf("mlx5_%d/1", first+i), 400))
	}
	return out
}

// hgxB200 is an HGX B200 node as observed in the field: eight 400 Gb/s
// RoCE rails, two 200 Gb/s front-end ports and four 100 Gb/s InfiniBand
// storage ports, with the given rails replaced.
func hgxB200(replace ...port) infiniband.PortMeasurement {
	all := rails(0, 8)
	all = append(all, active("mlx5_8/1", 200), active("mlx5_9/1", 200))
	for i := range 4 {
		all = append(all, port{key: fmt.Sprintf("mlx5_%d/1", 10+i), layer: "InfiniBand", state: 4, physState: 5, rate: 100})
	}
	for _, r := range replace {
		for i := range all {
			if all[i].key == r.key {
				all[i] = r
			}
		}
	}
	return ports(all...)
}

func portsOverride(name string, value any) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       fabricPortsCheck,
		ThresholdName: name,
		Value:         value,
	}))
}

func TestRDMAFabricPortsActiveDefinition(t *testing.T) {
	def := infiniband.RDMAFabricPortsActive()
	if def.ID != fabricPortsCheck {
		t.Errorf("ID = %q", def.ID)
	}
	if def.Category != health.InfiniBand {
		t.Errorf("Category = %v, want InfiniBand", def.Category)
	}
	want := []string{infiniband.SignalPortStates, infiniband.SignalPortPhysicalStates, infiniband.SignalPortRatesGbps}
	if !slices.Equal(def.RequiredSignals, want) {
		t.Errorf("RequiredSignals = %v, want %v", def.RequiredSignals, want)
	}
	if got := def.Architectures; !slices.Equal(got, []string{"B200"}) {
		t.Errorf("Architectures = %v, want [B200]", got)
	}
	if def.ConditionScope != health.NodeScope {
		t.Errorf("ConditionScope = %v, want NodeScope", def.ConditionScope)
	}
	for _, name := range []string{"expected_active_fabric_ports", "fabric_port_rate_gbps"} {
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

func TestRDMAFabricPortsActive(t *testing.T) {
	tests := []struct {
		name           string
		cat            func() *catalog.Catalog
		measurement    infiniband.PortMeasurement
		wantSeverity   health.Severity
		wantSkipped    bool
		wantSkipReason health.SkipReason
	}{
		{
			name:         "all eight rails up; front-end and storage ports ignored",
			measurement:  hgxB200(),
			wantSeverity: health.Pass,
		},
		{
			// The field case: three rails never brought up, which a
			// link-flap check cannot see.
			name:         "three rails administratively down",
			measurement:  hgxB200(down("mlx5_2/1", 400), down("mlx5_5/1", 400), down("mlx5_7/1", 400)),
			wantSeverity: health.Critical,
		},
		{
			name:         "one rail down",
			measurement:  hgxB200(down("mlx5_0/1", 400)),
			wantSeverity: health.Critical,
		},
		{
			// ACTIVE in the logical state alone is not enough.
			name:         "rail active but physical link not up",
			measurement:  hgxB200(port{key: "mlx5_3/1", layer: "Ethernet", state: 4, physState: 7, rate: 400}),
			wantSeverity: health.Critical,
		},
		{
			// A rail that trained down to a lower rate is no longer a
			// fabric port, so the count falls short.
			name:         "rail trained down to 200 Gb/s",
			measurement:  hgxB200(active("mlx5_4/1", 200)),
			wantSeverity: health.Critical,
		},
		{
			// Eight healthy rails plus more ports at fabric speed is fine.
			name:         "more fabric ports than expected",
			measurement:  ports(rails(0, 10)...),
			wantSeverity: health.Pass,
		},
		{
			name:         "rail missing from the state map is not counted",
			measurement:  func() infiniband.PortMeasurement { m := hgxB200(); delete(m.States, "mlx5_6/1"); return m }(),
			wantSeverity: health.Critical,
		},
		{
			name:         "InfiniBand rails count the same as RoCE",
			measurement:  ports(func() []port { r := rails(0, 8); r[0].layer = "InfiniBand"; return r }()...),
			wantSeverity: health.Pass,
		},
		{
			name:         "no ports at all",
			measurement:  ports(),
			wantSeverity: health.Critical,
		},
		{
			name:         "override tolerates a node built with six rails",
			cat:          func() *catalog.Catalog { return portsCatalog(portsOverride("expected_active_fabric_ports", int64(6))) },
			measurement:  ports(rails(0, 6)...),
			wantSeverity: health.Pass,
		},
		{
			name:         "override counts 200 Gb/s ports as fabric",
			cat:          func() *catalog.Catalog { return portsCatalog(portsOverride("fabric_port_rate_gbps", float64(200))) },
			measurement:  hgxB200(active("mlx5_4/1", 200)),
			wantSeverity: health.Pass,
		},
		{
			name:         "collection failure fails the check",
			measurement:  infiniband.ErroredPorts(errors.New("sysfs unreadable")),
			wantSeverity: health.Critical,
		},
		{
			name: "skipped on an unlisted architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
			},
			measurement:    hgxB200(),
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat := portsCatalog()
			if tc.cat != nil {
				cat = tc.cat()
			}
			v, err := cat.Evaluate(fabricPortsCheck, tc.measurement)
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
