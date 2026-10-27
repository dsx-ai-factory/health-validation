// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pciaddr parses and formats a GPU's PCI address in the form the
// driver prints in Xid lines.
package pciaddr

import (
	"fmt"
	"strconv"
	"strings"
)

// Address is a GPU's PCI domain, bus and device.
type Address struct {
	Domain uint32
	Bus    uint8
	Device uint8
}

// Parse reads "dddd:bb:dd" with a domain of 4 to 8 hex digits.
func Parse(s string) (Address, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 || len(parts[0]) < 4 || len(parts[0]) > 8 || len(parts[1]) != 2 || len(parts[2]) != 2 {
		return Address{}, false
	}
	domain, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return Address{}, false
	}
	bus, err := strconv.ParseUint(parts[1], 16, 8)
	if err != nil {
		return Address{}, false
	}
	device, err := strconv.ParseUint(parts[2], 16, 8)
	if err != nil || device > 0x1f {
		return Address{}, false
	}
	return Address{Domain: uint32(domain), Bus: uint8(bus), Device: uint8(device)}, true
}

// String formats a as the driver prints it in Xid lines (PCI:%04x:%02x:%02x),
// so an address read from NVML compares equal to one read from the journal.
func (a Address) String() string {
	return fmt.Sprintf("%04x:%02x:%02x", a.Domain, a.Bus, a.Device)
}
