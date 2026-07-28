/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

//go:build linux && !android

package nvml_test

import (
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/NVIDIA/health-validation/toolkit/nvml"
)

func TestNewWithLibrary_InitFailure(t *testing.T) {
	lib := &mock.Interface{
		InitFunc: func() gonvml.Return { return gonvml.ERROR_DRIVER_NOT_LOADED },
	}

	// Init is lazy: construction is side-effect free, so the failure surfaces
	// from the first measurement, not from NewWithLibrary.
	b := nvml.NewWithLibrary(lib)
	if _, err := b.MeasureGPUs(); err == nil {
		t.Fatal("expected init failure to surface from MeasureGPUs, got nil")
	}
}

// Device enumeration is deferred until the first measurement call —
// a broken DeviceGetCount or DeviceGetHandleByIndex must not prevent
// NewWithLibrary from returning a usable backend.
func TestNewWithLibrary_DefersDeviceEnumeration(t *testing.T) {
	var countCalled, handleCalled bool
	lib := &mock.Interface{
		InitFunc:     func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			countCalled = true
			return 0, gonvml.SUCCESS
		},
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			handleCalled = true
			return nil, gonvml.SUCCESS
		},
	}
	c := nvml.NewWithLibrary(lib)
	defer c.Close()
	if countCalled {
		t.Error("DeviceGetCount invoked before Measure — enumeration should be lazy")
	}
	if handleCalled {
		t.Error("DeviceGetHandleByIndex invoked before Measure — enumeration should be lazy")
	}
}

func TestNVMLBackend_Close(t *testing.T) {
	var shutdownCalled bool
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { shutdownCalled = true; return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 0, gonvml.SUCCESS },
	}

	c := nvml.NewWithLibrary(lib)
	if _, err := c.MeasureNode(); err != nil { // opens the library lazily
		t.Fatalf("MeasureNode: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
	if !shutdownCalled {
		t.Error("Close did not shut the library down after it was opened")
	}
}
