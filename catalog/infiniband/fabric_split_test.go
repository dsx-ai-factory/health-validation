// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// The two fabric checks divide the work the way gpu_count and the per-GPU
// checks do: rdma_fabric_port_count asks whether the node exposes its rails,
// ib_port_state asks whether each rail is healthy. They fire together on no
// scenario below, so one fault is reported once.
func TestFabricChecksDivideTheWork(t *testing.T) {
	tests := []struct {
		name string
		// ports is the node as the sensor saw it.
		ports []infiniband.PortMeasurement
		// stateNames lists the ports ib_port_state must name; empty means it passes.
		stateNames []string
		countFails bool
	}{
		{
			name:  "healthy node",
			ports: hgxB200(),
		},
		{
			// The field failure: down rails still enumerate at their
			// configured rate, so only the per-port check sees them.
			name: "three rails administratively down",
			ports: hgxB200(
				downNIC("mlx5_2", "Ethernet", 400), downNIC("mlx5_5", "Ethernet", 400), downNIC("mlx5_7", "Ethernet", 400),
			),
			stateNames: []string{"mlx5_2/1", "mlx5_5/1", "mlx5_7/1"},
		},
		{
			name:       "a rail missing from sysfs",
			ports:      without(hgxB200(), "mlx5_6"),
			countFails: true,
		},
		{
			// ACTIVE and LinkUp at 200 Gb/s: healthy as a port, but no longer
			// a fabric rail, so only the count notices.
			name:       "a rail trained down to 200 Gb/s",
			ports:      hgxB200(nic("mlx5_4", "Ethernet", 200)),
			countFails: true,
		},
		{
			name:  "an uncabled storage port",
			ports: hgxB200(downNIC("mlx5_10", "InfiniBand", 100)),
		},
		{
			name:  "an uncabled front-end port",
			ports: hgxB200(downNIC("mlx5_8", "Ethernet", 200)),
		},
		{
			// A spare rail beyond the expected eight absorbs the loss for the
			// count, but the down port is still a fault on its own.
			name: "one of ten rails down",
			ports: append(hgxB200(downNIC("mlx5_1", "Ethernet", 400)),
				nic("mlx5_14", "Ethernet", 400), nic("mlx5_15", "Ethernet", 400)),
			stateNames: []string{"mlx5_1/1"},
		},
	}
	cat := b200Catalog()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count, err := cat.Evaluate(fabricCountCheck, infiniband.InventoryOf(tc.ports))
			if err != nil {
				t.Fatal(err)
			}
			if got := count.Severity == health.Critical; got != tc.countFails {
				t.Errorf("%s critical = %v, want %v (messages %v)", fabricCountCheck, got, tc.countFails, count.Messages)
			}

			var named []string
			for _, p := range tc.ports {
				v, err := cat.Evaluate("ib_port_state", p)
				if err != nil {
					t.Fatal(err)
				}
				if v.Severity == health.Critical {
					named = append(named, v.AffectedEntities...)
				}
			}
			slices.Sort(named)
			if !slices.Equal(named, tc.stateNames) && (len(named) != 0 || len(tc.stateNames) != 0) {
				t.Errorf("ib_port_state names %v, want %v", named, tc.stateNames)
			}

			if tc.countFails && len(named) != 0 {
				t.Errorf("one fault reported by both checks: count fails and ib_port_state names %v", named)
			}
		})
	}
}
