// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

func TestInventoryOf_KeysPortsByEntity(t *testing.T) {
	inv := infiniband.InventoryOf([]infiniband.PortMeasurement{
		nic("mlx5_0", "Ethernet", 400),
		downNIC("mlx5_1", "Ethernet", 400),
	})
	if len(inv.Errors()) != 0 {
		t.Fatalf("Errors = %v, want none", inv.Errors())
	}
	want := map[string]float64{"mlx5_0/1": 400, "mlx5_1/1": 400}
	if len(inv.RatesGbps) != len(want) {
		t.Fatalf("RatesGbps = %v, want %v", inv.RatesGbps, want)
	}
	for k, v := range want {
		if inv.RatesGbps[k] != v {
			t.Errorf("RatesGbps[%q] = %v, want %v", k, inv.RatesGbps[k], v)
		}
	}
}

// A port whose rate could not be read is left out, so it never counts as a
// fabric rail; the rest of the node still evaluates.
func TestInventoryOf_UnreadableRateLeavesPortOut(t *testing.T) {
	bad := nic("mlx5_3", "Ethernet", 400)
	bad.AddError(errors.New("rate: permission denied"), infiniband.SignalIBRateGbps)

	inv := infiniband.InventoryOf([]infiniband.PortMeasurement{nic("mlx5_0", "Ethernet", 400), bad})
	if len(inv.Errors()) != 0 {
		t.Errorf("Errors = %v, want none for a per-port failure", inv.Errors())
	}
	if _, ok := inv.RatesGbps["mlx5_3/1"]; ok {
		t.Errorf("RatesGbps = %v, want mlx5_3/1 left out", inv.RatesGbps)
	}
	if _, ok := inv.RatesGbps["mlx5_0/1"]; !ok {
		t.Errorf("RatesGbps = %v, want mlx5_0/1 kept", inv.RatesGbps)
	}
}

// A failure that names no device is the whole source failing.
func TestInventoryOf_SourceFailureMarksInventoryErrored(t *testing.T) {
	src := infiniband.ErroredPort("", 0, errors.New("sysfs unreadable"), infiniband.PortMeasurementSignals...)
	inv := infiniband.InventoryOf([]infiniband.PortMeasurement{src})
	if len(inv.Errors()) == 0 {
		t.Fatal("Errors = none, want the source failure carried over")
	}
	if got := inv.Errors()[0].FailedSignals(); len(got) != 1 || got[0] != infiniband.SignalPortRatesGbps {
		t.Errorf("FailedSignals = %v, want [%s]", got, infiniband.SignalPortRatesGbps)
	}
}

func TestPortInventory_SignalsDisjointFromPortSignals(t *testing.T) {
	port := map[string]bool{}
	for _, s := range infiniband.PortMeasurementSignals {
		port[s] = true
	}
	for _, s := range infiniband.PortInventorySignals() {
		if port[s] {
			t.Errorf("signal %q is carried by both port measurement types", s)
		}
	}
	if (infiniband.PortInventoryMeasurement{}).Category() != health.InfiniBand {
		t.Error("PortInventoryMeasurement must be an InfiniBand measurement")
	}
}
