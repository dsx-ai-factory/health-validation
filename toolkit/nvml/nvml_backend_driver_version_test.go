// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"errors"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

func TestNVMLBackend_DriverVersion(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:                   func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:               func() gonvml.Return { return gonvml.SUCCESS },
		SystemGetDriverVersionFunc: func() (string, gonvml.Return) { return "575.51.03", gonvml.SUCCESS },
	}
	b := nvml.NewWithLibrary(lib)
	defer b.Close()

	got, err := b.DriverVersion()
	if err != nil {
		t.Fatalf("DriverVersion: %v", err)
	}
	if got != "575.51.03" {
		t.Errorf("DriverVersion = %q, want 575.51.03", got)
	}
}

func TestNVMLBackend_DriverVersionError(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:                   func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:               func() gonvml.Return { return gonvml.SUCCESS },
		SystemGetDriverVersionFunc: func() (string, gonvml.Return) { return "", gonvml.ERROR_UNKNOWN },
	}
	b := nvml.NewWithLibrary(lib)
	defer b.Close()

	_, err := b.DriverVersion()
	var nvmlErr *nvml.Error
	if !errors.As(err, &nvmlErr) || nvmlErr.Op != "SystemGetDriverVersion" {
		t.Errorf("DriverVersion error = %v, want an *nvml.Error for SystemGetDriverVersion", err)
	}
}

func TestNVMLBackend_DriverVersionInitFailure(t *testing.T) {
	lib := &mock.Interface{
		InitFunc: func() gonvml.Return { return gonvml.ERROR_DRIVER_NOT_LOADED },
	}
	if _, err := nvml.NewWithLibrary(lib).DriverVersion(); err == nil {
		t.Error("DriverVersion error = nil, want the init failure")
	}
}
