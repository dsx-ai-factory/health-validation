// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// A failing per-port verdict names its port; a device-level failure names the
// device; an unidentified port is omitted rather than recorded blank.
func TestPortMeasurementEntityID(t *testing.T) {
	var _ health.EntityIdentifier = infiniband.PortMeasurement{}
	for _, tc := range []struct {
		name string
		want string
		m    infiniband.PortMeasurement
	}{
		{name: "port", m: port("ACTIVE", "LinkUp"), want: "mlx5_0/1"},
		{name: "device only", m: infiniband.ErroredPort("mlx5_2", 0, errors.New("ports unreadable"), infiniband.PortMeasurementSignals...), want: "mlx5_2"},
		{name: "unknown", m: infiniband.ErroredPort("", 0, errors.New("sysfs unreadable"), infiniband.PortMeasurementSignals...)},
	} {
		if got := tc.m.EntityID(); got != tc.want {
			t.Errorf("%s: EntityID() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A down port's verdict carries the port in AffectedEntities, which is what
// feeds affected_components[].entities and the affected-entities metric.
func TestPortStateVerdictNamesThePort(t *testing.T) {
	m := port("DOWN", "Polling")
	m.Device, m.Port = "mlx5_2", 1
	v, err := ibCatalog().Evaluate("ib_port_state", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.AffectedEntities) != 1 || v.AffectedEntities[0] != "mlx5_2/1" {
		t.Errorf("AffectedEntities = %v, want [mlx5_2/1]", v.AffectedEntities)
	}
}
