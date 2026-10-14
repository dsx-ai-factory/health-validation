// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package device provides a wrapper around the "github.com/NVIDIA/go-nvlib/pkg/nvlib/device".Device
// type that adds PCIBusID and UUID methods.
package device

import (
	"fmt"

	"github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Device is a wrapper around the "github.com/NVIDIA/go-nvlib/pkg/nvlib/device".Device
// type that adds PCIBusID and UUID methods, plus the driver-gated
// GetMarginTemperature. Per-device NVML queries such as GetTemperature come
// from the embedded interface.
type Device interface {
	device.Device
	PCIBusID() string
	UUID() string
	GetMarginTemperature() (nvml.MarginTemperature, nvml.Return)
}

var _ Device = &nvDevice{}

type nvDevice struct {
	device.Device
	busID       string
	uuid        string
	driverMajor int
}

func (d *nvDevice) PCIBusID() string {
	return d.busID
}

func (d *nvDevice) UUID() string {
	return d.uuid
}

func New(dev device.Device, busID string, opts ...OpOption) Device {
	op := &Op{}
	op.applyOpts(opts)

	uuid, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		panic(fmt.Sprintf("failed to get device UUID: %v", nvml.ErrorString(ret)))
	}
	return &nvDevice{Device: dev, busID: busID, uuid: uuid, driverMajor: op.DriverMajor}
}

// Op holds device options.
type Op struct {
	// DriverMajor gates driver-version-sensitive NVML calls such as the
	// margin temperature API.
	DriverMajor int
}

// OpOption is a function that configures the Op struct.
type OpOption func(*Op)

func (op *Op) applyOpts(opts []OpOption) {
	for _, opt := range opts {
		opt(op)
	}
}

// WithDriverMajor sets the driver major version used to gate
// driver-version-sensitive NVML calls.
func WithDriverMajor(major int) OpOption {
	return func(op *Op) {
		op.DriverMajor = major
	}
}
