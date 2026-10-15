// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package lib implements the NVIDIA Management Library (NVML) interface.
// See https://docs.nvidia.com/deploy/nvml-api/nvml-api-reference.html#nvml-api-reference for more details.
package lib

import (
	nvlibdevice "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	nvinfo "github.com/NVIDIA/go-nvlib/pkg/nvlib/info"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type Library interface {
	NVML() nvml.Interface
	Device() nvlibdevice.Interface
	Info() nvinfo.Interface
	Shutdown() nvml.Return
}

var (
	_ Library        = &nvmlInterface{}
	_ nvml.Interface = &nvmlInterface{}
)

type nvmlInterface struct {
	nvml.Interface

	dev  nvlibdevice.Interface
	info nvinfo.Interface
}

func (n *nvmlInterface) NVML() nvml.Interface {
	return n
}

func (n *nvmlInterface) Device() nvlibdevice.Interface {
	return n.dev
}

func (n *nvmlInterface) Info() nvinfo.Interface {
	return n.info
}

func (n *nvmlInterface) Shutdown() nvml.Return {
	return n.Interface.Shutdown()
}

// createLibrary creates a new NVML library handle from the given options.
func createLibrary(opts ...OpOption) Library {
	options := &Op{}
	options.applyOpts(opts)

	nvInterface := &nvmlInterface{Interface: options.nvmlLib}
	nvInterface.dev = nvlibdevice.New(nvInterface.Interface)
	nvInterface.info = nvinfo.New(
		nvinfo.WithNvmlLib(nvInterface),
		nvinfo.WithDeviceLib(nvInterface.dev),
	)
	return nvInterface
}
