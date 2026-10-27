// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pci exposes read-only PCI inventory, link properties, and raw ACS registers.
// The sensor ID is pci.sysfs. Device identity is carried in target.pci.address.
//
// # Link properties
//
// Link observations supplement the address, vendor, device, class, driver,
// module, GPU-presence, and ACS signals:
//
//   - pci.link.speed: current_link_speed as a double in GT/s.
//   - pci.link.speed.max: max_link_speed as a double in GT/s.
//   - pci.link.width: current_link_width as an int64 lane count.
//   - pci.link.width.max: max_link_width as an int64 lane count.
//   - pci.link.generation: generation derived from current speed, as a uint64.
//   - pci.link.generation.max: generation derived from maximum speed, as a uint64.
//
// Widths of 0 or 255 produce no_value with NOT_SUPPORTED, covering devices
// without link registers and the kernel's unknown-width sentinel. Negative
// widths are malformed. Speeds of 2.5, 5, 8, 16, 32, 64, and 128 GT/s map to
// generations 1 through 7. "Unknown" and "Unknown speed" produce no value for
// speed and generation. An unrecognized numeric speed is retained, while its
// generation is unsupported. Generation is never inferred from width or
// another device's speed.
//
// Missing link attributes on an existing device produce no_value with
// NOT_SUPPORTED. Permission errors, malformed values, other read failures,
// and dangling links remain collection errors. Reads are bounded and symlink
// resolution is confined to the supplied filesystem. These observations add
// no catalog checks.
package pci
