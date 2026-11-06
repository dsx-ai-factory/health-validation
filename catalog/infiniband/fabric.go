// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// fabricRateThreshold defines which ports count as fabric rails: those at or
// above this rate. Rate separates the per-GPU fabric NICs from front-end and
// storage NICs on the same node without naming devices, and one floor covers
// HGX B200 (400 Gb/s), GB200 (400) and GB300 (800).
//
// Every check that scopes itself to fabric ports declares this spec, so the
// class is defined once. Overrides are per check, so a fleet that moves the
// floor sets it on each.
func fabricRateThreshold() health.ThresholdSpec {
	return health.ThresholdSpec{
		Value:       float64(400),
		ValueType:   health.ThresholdFloat64,
		ValueMin:    float64(1),
		Category:    health.DefaultThreshold,
		Description: "Minimum port rate, in Gb/s, for a port to count as a fabric rail: 400 for the NDR / 400GbE rails on HGX B200 and GB200. Ports below it, such as front-end or storage NICs, are ignored.",
		Unit:        "Gb/s",
	}
}
