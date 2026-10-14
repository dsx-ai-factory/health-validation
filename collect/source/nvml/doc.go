// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package nvml is the NVML source client for the collect module: one
// lifecycle owner, device enumeration exposed for the coordinator's
// per-cycle shared read.
//
// Ported from github.com/leptonai/gpud pkg/nvidia/nvml (Apache-2.0),
// origin/main @ 027f5f5bf5da97f627e08798f97eebf8078f4636, cut to the
// absolute minimum needed to initialize NVML, enumerate devices, and read
// per-device signals such as temperature. Dropped from the upstream package
// and to be re-added only when a sensor needs them: fabric state, product
// capability tables, brand/architecture/driver-version helpers, failure
// injection and test fixtures, and the eventstore-backed slowdown-event
// option (storage stays out of collect). The driver-gated margin
// temperature query stays because the temperature sensor needs it.
// gpud's zap-based pkg/log calls are now log/slog.
//
// Known sharp edge kept from upstream for fidelity: device.New panics if a
// device UUID cannot be read. The upstream fixed-at-construction device map is
// already fixed here: Instance.Refresh re-enumerates devices on the existing
// library handle for the coordinator's per-cycle shared read.
package nvml
