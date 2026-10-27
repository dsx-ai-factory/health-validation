// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
)

var (
	pciAddressRE = regexp.MustCompile(`(?i)^[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f](?:\.0)?$`)
	xidAddressRE = regexp.MustCompile(`NVRM: Xid \((?:PCI:)?([0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[01][0-9a-fA-F](?:\.0)?)(?:[ )\s]|$)`)
)

type timeRanges struct {
	from    *int64
	to      *int64
	gpuFrom map[string]int64
}

// NewWithTimeRanges filters the current boot's Xid entries to exclusive Unix
// second bounds. Nil bounds are unbounded. gpuFrom replaces from for the
// specified domain-qualified PCI addresses (dddd:bb:dd, optionally .0).
func NewWithTimeRanges(reader *journal.Reader, from, to *int64, gpuFrom map[string]int64) (collect.Sensor, error) {
	if from != nil && *from < 0 {
		return nil, fmt.Errorf("--xid-from must be a non-negative Unix timestamp in seconds")
	}
	if to != nil && *to <= 0 {
		return nil, fmt.Errorf("--xid-to must be a positive Unix timestamp in seconds")
	}
	if from != nil && to != nil && *from >= *to {
		return nil, fmt.Errorf("--xid-from must be less than --xid-to")
	}
	ranges := timeRanges{gpuFrom: make(map[string]int64, len(gpuFrom))}
	if from != nil {
		value := *from
		ranges.from = &value
	}
	if to != nil {
		value := *to
		ranges.to = &value
	}
	for address, timestamp := range gpuFrom {
		if !pciAddressRE.MatchString(address) {
			return nil, fmt.Errorf("--xid-from: invalid GPU PCI address %q (expected dddd:bb:dd or dddd:bb:dd.0)", address)
		}
		address = normalizePCIAddress(address)
		if _, exists := ranges.gpuFrom[address]; exists {
			return nil, fmt.Errorf("--xid-from: duplicate GPU PCI address %q", address)
		}
		if timestamp < 0 {
			return nil, fmt.Errorf("--xid-from for GPU %s must be a non-negative Unix timestamp in seconds", address)
		}
		if to != nil && timestamp >= *to {
			return nil, fmt.Errorf("--xid-from for GPU %s must be less than --xid-to", address)
		}
		ranges.gpuFrom[address] = timestamp
	}
	s := New(reader).(*sensor)
	s.ranges = ranges
	return s, nil
}

func normalizePCIAddress(address string) string {
	return strings.TrimSuffix(strings.ToLower(address), ".0")
}

func (r timeRanges) excludes(at time.Time, message string) bool {
	if r.to != nil && at.Unix() >= *r.to {
		return true
	}
	from := r.from
	if address := xidAddressRE.FindStringSubmatch(message); address != nil {
		if timestamp, ok := r.gpuFrom[normalizePCIAddress(address[1])]; ok {
			from = &timestamp
		}
	} else if from != nil {
		// Without a recoverable address, exclude only entries older than
		// every GPU's lower bound; an override may preserve older history.
		for _, timestamp := range r.gpuFrom {
			if timestamp < *from {
				value := timestamp
				from = &value
			}
		}
	}
	return from != nil && (at.Unix() < *from || (at.Unix() == *from && at.Nanosecond() == 0))
}

func (r timeRanges) bounded() bool {
	return r.from != nil || r.to != nil || len(r.gpuFrom) > 0
}

func (r timeRanges) description() string {
	window := "boot"
	if r.from != nil {
		window += fmt.Sprintf(" from=%d", *r.from)
	}
	if r.to != nil {
		window += fmt.Sprintf(" to=%d", *r.to)
	}
	addresses := make([]string, 0, len(r.gpuFrom))
	for address := range r.gpuFrom {
		addresses = append(addresses, address)
	}
	slices.Sort(addresses)
	for _, address := range addresses {
		window += fmt.Sprintf(" %s:from=%d", address, r.gpuFrom[address])
	}
	return window
}
