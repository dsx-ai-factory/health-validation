// Copyright 2019 The Prometheus Authors
// SPDX-License-Identifier: Apache-2.0
// Copied from GPUd components/accelerator/nvidia/infiniband/class/class.go
// at ea5ab612abad200c7793220f0e7955bb7c8f6302, derived from prometheus/procfs.
// Only finite/range validation is added to parseRate.

package infiniband

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func parseState(s string) (uint, string, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("failed to split %s into 'ID: NAME'", s)
	}
	name := strings.TrimSpace(parts[1])
	value, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return 0, name, fmt.Errorf("failed to convert %s into uint", strings.TrimSpace(parts[0]))
	}
	id := uint(value)
	return id, name, nil
}

// parseRate parses rate (example: "100 Gb/sec (4X EDR)") and return it as bytes/second.
// It returns the rate in GB/s and the rate in bytes/second.
func parseRate(s string) (float64, uint64, error) {
	parts := strings.SplitAfterN(s, " ", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("failed to split %q", s)
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 32)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to convert %s into uint", strings.TrimSpace(parts[0]))
	}

	// Reject values that cannot be represented instead of converting NaN,
	// negative values, or overflow to an implementation-dependent integer.
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value*125000000 >= math.Exp2(64) {
		return 0, 0, fmt.Errorf("invalid port rate %q", s)
	}
	// Convert Gb/s into bytes/s
	rate := uint64(value * 125000000)
	return value, rate, nil
}
