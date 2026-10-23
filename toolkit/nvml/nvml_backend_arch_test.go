// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

func TestArchitectureFromName(t *testing.T) {
	cases := map[string]string{
		"NVIDIA GB300":            "GB300",
		"NVIDIA GB200":            "GB200",
		"NVIDIA B200":             "B200",
		"NVIDIA H100 80GB HBM3":   "H100",
		"NVIDIA H200":             "H200",
		"NVIDIA A100-SXM4-80GB":   "A100",
		"NVIDIA GeForce RTX 4090": "",
		"":                        "",
	}
	for name, want := range cases {
		if got := architectureFromName(name); got != want {
			t.Errorf("architectureFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

// Architecture reports the family of the first enumerated device whose
// name reads, mapped to a catalog architecture identifier.
func TestArchitecture_ReadsFirstNamedDevice(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 1, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetNameFunc: func() (string, gonvml.Return) { return "NVIDIA GB300", gonvml.SUCCESS },
			}, gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	arch, err := b.Architecture()
	if err != nil {
		t.Fatalf("Architecture: %v", err)
	}
	if arch != "GB300" {
		t.Errorf("Architecture = %q, want GB300", arch)
	}
}

// An unrecognized product name yields architectureUnknown so
// architecture-scoped checks skip rather than run against the wrong
// thresholds.
func TestArchitecture_UnrecognizedNameYieldsUnknown(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 1, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetNameFunc: func() (string, gonvml.Return) { return "NVIDIA GeForce RTX 4090", gonvml.SUCCESS },
			}, gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	arch, err := b.Architecture()
	if err != nil {
		t.Fatalf("Architecture: %v", err)
	}
	if arch != architectureUnknown {
		t.Errorf("Architecture = %q, want %q", arch, architectureUnknown)
	}
}

// When every device's name is unreadable (e.g. all GPUs lost), Architecture
// returns an error, not architectureUnknown: a dead node must not skip its way
// to PASS. "unknown" is reserved for names that read but aren't recognized.
func TestArchitecture_AllNamesUnreadableReturnsError(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 1, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetNameFunc: func() (string, gonvml.Return) { return "", gonvml.ERROR_GPU_IS_LOST },
			}, gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	if arch, err := b.Architecture(); err == nil {
		t.Fatalf("Architecture = %q, nil error; want an error when no GPU name reads", arch)
	}
}

// A node that enumerates zero devices is a discovery failure, not "unknown":
// architecture-scoped checks would otherwise all skip and aggregate to PASS.
func TestArchitecture_ZeroDevicesReturnsError(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 0, gonvml.SUCCESS },
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	if arch, err := b.Architecture(); err == nil {
		t.Fatalf("Architecture = %q, nil error; want an error when zero devices enumerate", arch)
	}
}

// End to end: on an 8-GPU node whose product name is unrecognized,
// architecture-scoped checks (here gpu_count, default expected 4) must skip,
// not fire CRITICAL — the fail-open this guards against would drain a healthy
// node.
func TestArchitecture_UnknownArchSkipsScopedChecks(t *testing.T) {
	const gpuCount = 8
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return gpuCount, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetNameFunc: func() (string, gonvml.Return) { return "NVIDIA GeForce RTX 4090", gonvml.SUCCESS },
			}, gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	arch, err := b.Architecture()
	if err != nil {
		t.Fatalf("Architecture: %v", err)
	}

	cat := catalog.New(catalog.WithArchitecture(arch), catalog.WithExecutionContext(health.OperatorInfra))
	v, err := cat.Evaluate("gpu_count", node.NodeMeasurement{GPUCount: gpuCount})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("gpu_count: skipped=%v reason=%v, want skipped ArchitectureUnsupported (fail safe, not CRITICAL)",
			v.Skipped, v.SkipReason)
	}
}
