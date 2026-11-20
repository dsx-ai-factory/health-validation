// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
)

func TestCollectorWiresSensorWithoutHardware(t *testing.T) {
	sensor := cpusensor.New(fstest.MapFS{
		"sys/devices/system/cpu/possible": {Data: []byte("0-7")},
		"sys/devices/system/cpu/online":   {Data: []byte("0-7")},
		"sys/devices/system/node/online":  {Data: []byte("0-1")},
	})
	collector := newCollector(sensor)
	batch, err := collector.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	if batch.GetCollectionId() == "" {
		t.Fatal("missing per-request collection ID")
	}
	want := map[string]int64{cpusignals.PossibleCount.ID(): 8, cpusignals.OnlineCount.ID(): 8, cpusignals.NUMANodeOnlineCount.ID(): 2}
	for _, current := range batch.GetObservations() {
		if expected, ok := want[current.GetSignalId()]; ok {
			if current.GetValue() == nil || current.GetValue().GetIntValue() != expected {
				t.Errorf("%s = %v, want %d", current.GetSignalId(), current.GetValue(), expected)
			}
			delete(want, current.GetSignalId())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing signals: %v", want)
	}
	second, err := collector.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.GetCollectionId() == batch.GetCollectionId() {
		t.Fatal("requests reused a collection ID")
	}
}
