// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"

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

type releaseNVMLInstance struct {
	nvmlsource.Instance
	shutdownErr error
	shutdowns   int
}

func (i *releaseNVMLInstance) Shutdown() error { i.shutdowns++; return i.shutdownErr }

func TestReleaseCycleDropsNVMLHandlesAndCachedReaders(t *testing.T) {
	first := &releaseNVMLInstance{Instance: stalledInstance(make(chan struct{}), false)}
	host := &nvmlHost{inst: first}
	host.inventory = gpuinventory.New(gpuinventory.OpenNVML(host.instance))
	oldInventory := host.gpuInventory()
	oldSensor, err := host.sharedNVMLSensor()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if releaseErr := host.releaseCycle(); releaseErr != nil {
			t.Fatal(releaseErr)
		}
	}
	if first.shutdowns != 1 || host.inst != nil || host.nvmlSensor != nil || host.nvmlSensorInst != nil {
		t.Fatal("idle cycle retained native handles or repeated shutdown")
	}
	if host.gpuInventory() == oldInventory {
		t.Fatal("retained inventory reader over a closed instance")
	}
	replacement := stalledInstance(make(chan struct{}), false)
	replacement.records = replacement.records[:2]
	host.inst = replacement
	nextSensor, err := host.sharedNVMLSensor()
	if err != nil || nextSensor == oldSensor || host.nvmlSensorInst != replacement {
		t.Fatalf("replacement sensor = %v, %v", nextSensor, err)
	}
	cycle, err := collect.NewCycle("fresh-after-idle", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := host.gpuInventory().Snapshot(context.Background(), cycle)
	if count, ok := gpuinventory.Count(snapshot); !ok || count != 2 {
		t.Fatalf("new inventory reused closed instance: count=%d, valid=%v", count, ok)
	}
}

func TestReleaseCycleRetainsFailedShutdownForRetry(t *testing.T) {
	for _, failure := range []error{nvmlsource.ErrInUse, errors.New("shutdown failed")} {
		inst := &releaseNVMLInstance{Instance: stalledInstance(make(chan struct{}), false), shutdownErr: failure}
		host := &nvmlHost{inst: inst}
		host.inventory = gpuinventory.New(gpuinventory.OpenNVML(host.instance))
		oldInventory := host.gpuInventory()
		oldSensor, err := host.sharedNVMLSensor()
		if err != nil {
			t.Fatal(err)
		}
		if err := host.releaseCycle(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if host.inst != inst || host.nvmlSensor != oldSensor || host.gpuInventory() != oldInventory {
			t.Fatal("discarded ownership before native shutdown succeeded")
		}
		inst.shutdownErr = nil
		if releaseErr := host.releaseCycle(); releaseErr != nil {
			t.Fatal(releaseErr)
		}
		if inst.shutdowns != 2 || host.inst != nil {
			t.Fatal("shutdown retry did not release instance")
		}
	}
}
