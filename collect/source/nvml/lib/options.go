// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	// EnvNVMLLibraryPath explicitly selects the NVML shared library.
	EnvNVMLLibraryPath = "GPUD_NVML_LIBRARY_PATH"
	// EnvNVIDIADriverRoot points at a containerized NVIDIA driver installation,
	// such as the GPU Operator's /run/nvidia/driver tree.
	EnvNVIDIADriverRoot = "GPUD_NVIDIA_DRIVER_ROOT"
)

type Op struct {
	nvmlLib nvml.Interface
}

type OpOption func(*Op)

func (op *Op) applyOpts(opts []OpOption) {
	for _, opt := range opts {
		opt(op)
	}
	if op.nvmlLib == nil {
		libraryPath := resolveNVMLLibraryPath()
		if libraryPath == "" {
			op.nvmlLib = nvml.New()
		} else {
			op.nvmlLib = nvml.New(nvml.WithLibraryPath(libraryPath))
		}
	}
}

// resolveNVMLLibraryPath returns an explicitly configured library first, then
// searches a configured driver root. With neither environment variable set it
// returns empty, preserving go-nvml's standard system-library lookup.
func resolveNVMLLibraryPath() string {
	if libraryPath := os.Getenv(EnvNVMLLibraryPath); libraryPath != "" {
		return libraryPath
	}

	driverRoot := os.Getenv(EnvNVIDIADriverRoot)
	if driverRoot == "" {
		return ""
	}

	archLibraryDir := nvmlArchLibraryDir(runtime.GOARCH)
	candidates := []string{
		filepath.Join(driverRoot, "usr", "lib", archLibraryDir, "libnvidia-ml.so.1"),
		filepath.Join(driverRoot, "usr", "lib64", "libnvidia-ml.so.1"),
		filepath.Join(driverRoot, "usr", "lib", "libnvidia-ml.so.1"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func nvmlArchLibraryDir(goarch string) string {
	switch goarch {
	case "arm64":
		return "aarch64-linux-gnu"
	case "ppc64le":
		return "powerpc64le-linux-gnu"
	default:
		return "x86_64-linux-gnu"
	}
}

// WithNVML specifies the NVML library instance.
// Otherwise, defaults to the NVML library instance returned by nvml.New().
func WithNVML(nvmlLib nvml.Interface) OpOption {
	return func(op *Op) {
		op.nvmlLib = nvmlLib
	}
}
