// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

var ErrNVMLNotFound = errors.New("NVML not found")

// New instantiates a new NVML instance and initializes the NVML library.
// It returns ErrNVMLNotFound if NVML is not installed or the driver is not loaded.
func New(opts ...OpOption) (Library, error) {
	lib := createLibrary(opts...)
	ret := lib.NVML().Init()
	if ret == nvml.SUCCESS {
		return lib, nil
	}
	if ret == nvml.ERROR_LIBRARY_NOT_FOUND {
		return nil, ErrNVMLNotFound
	}

	// e.g., "Driver Not Loaded"
	es := nvml.ErrorString(ret)
	if strings.Contains(strings.ToLower(es), "driver not loaded") {
		return nil, ErrNVMLNotFound
	}

	return nil, fmt.Errorf("failed to initialize NVML: %s", es)
}
