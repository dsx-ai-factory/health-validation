// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// GetSystemDriverVersion returns the system driver version through NVML.
func GetSystemDriverVersion(nvmlLib nvml.Interface) (string, error) {
	ver, ret := nvmlLib.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get driver version: %v", nvml.ErrorString(ret))
	}
	return ver, nil
}

// ParseDriverVersion parses the NVML driver version into its numeric parts.
func ParseDriverVersion(version string) (major, minor, patch int, err error) {
	splits := strings.Split(version, ".")
	if len(splits) < 2 {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (expected at least 2 parts): %v", version)
	}
	if len(splits) > 3 {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (expected at most 3 parts): %v", version)
	}

	major, err = strconv.Atoi(splits[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (major): %v", err)
	}
	minor, err = strconv.Atoi(splits[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (minor): %v", err)
	}
	patch = 0
	if len(splits) > 2 {
		patch, err = strconv.Atoi(splits[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("failed to parse driver version (patch): %v", err)
		}
	}

	return major, minor, patch, nil
}
