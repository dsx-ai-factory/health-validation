// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"testing"

	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

func TestOpenInventoryReusesLateCompletedInitialization(t *testing.T) {
	inst := stalledInstance(make(chan struct{}), false)
	host := &nvmlHost{inst: inst}
	reader, err := gpuinventory.OpenNVML(host.instance)()
	if err != nil {
		t.Fatal(err)
	}
	if host.inst != inst {
		t.Fatal("open replaced an existing initialized source")
	}
	snapshot, err := reader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count, ok := gpuinventory.Count(snapshot); !ok || count != 4 {
		t.Fatalf("count = %d, %v, want the retained source's four GPUs", count, ok)
	}
}

func TestSharedNVMLSensorTracksInstance(t *testing.T) {
	first := stalledInstance(make(chan struct{}), false)
	host := &nvmlHost{inst: first}
	sensor, err := host.sharedNVMLSensor()
	if err != nil {
		t.Fatal(err)
	}
	reused, err := host.sharedNVMLSensor()
	if err != nil || reused != sensor {
		t.Fatalf("sensor was not reused: %v", err)
	}
	second := stalledInstance(make(chan struct{}), false)
	host.inst = second
	rebuilt, err := host.sharedNVMLSensor()
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt == sensor || host.nvmlSensorInst != second {
		t.Fatal("sensor did not rebuild for the replacement instance")
	}
}
