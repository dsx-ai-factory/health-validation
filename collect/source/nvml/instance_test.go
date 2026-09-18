// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"errors"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	nvmllib "github.com/dsx-ai-factory/health-validation/collect/source/nvml/lib"
)

type shutdownLibrary struct {
	nvmllib.Library
	shutdowns int
}

func (l *shutdownLibrary) Shutdown() nvml.Return { l.shutdowns++; return nvml.SUCCESS }

func TestNativeLifecycleGate(t *testing.T) {
	lib := &shutdownLibrary{}
	inst := &instance{nvmlLib: lib}
	other := &instance{}
	err := inst.Read(func(map[string]device.Device) error {
		if readErr := other.Read(func(map[string]device.Device) error { t.Fatal("entered native read twice"); return nil }); !errors.Is(readErr, ErrInUse) {
			t.Errorf("overlap: %v", readErr)
		}
		if refreshErr := inst.Refresh(); !errors.Is(refreshErr, ErrInUse) {
			t.Errorf("refresh during read: %v", refreshErr)
		}
		if shutdownErr := inst.Shutdown(); !errors.Is(shutdownErr, ErrInUse) {
			t.Errorf("shutdown during read: %v", shutdownErr)
		}
		return nil
	})
	if err != nil || lib.shutdowns != 0 {
		t.Fatalf("unsafe shutdown: %d / %v", lib.shutdowns, err)
	}
	for range 2 {
		if shutdownErr := inst.Shutdown(); shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	}
	if lib.shutdowns != 1 {
		t.Fatalf("shutdown count: %d", lib.shutdowns)
	}
	if readErr := inst.Read(func(map[string]device.Device) error { t.Fatal("read closed instance"); return nil }); !errors.Is(readErr, errClosed) {
		t.Fatalf("closed read: %v", readErr)
	}
	if refreshErr := inst.Refresh(); !errors.Is(refreshErr, errClosed) {
		t.Fatalf("closed refresh: %v", refreshErr)
	}
}
