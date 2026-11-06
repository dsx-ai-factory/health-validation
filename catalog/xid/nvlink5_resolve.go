// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Immediate-action buckets NVIDIA's Xid 144-150 decode table assigns.
// IGNORE and RESET_GPU are actions; the rest describe what happened when
// a row declined to name one.
const (
	resolutionIgnore   = "IGNORE"
	resolutionResetGPU = "RESET_GPU"

	// resolutionResetGPUUnconfirmed is a reset NVIDIA's table does not
	// confirm: either the Fatal flag implies it because no row covers a
	// (subcode, ErrorStatus/IntrInfo) combination, or a row escalates to
	// it on a register bit that cannot be located without the driver
	// era. Without this the verdict could not distinguish "the table
	// says reset this GPU" from a conservative guess. Both still
	// escalate, but only one of them is documented guidance.
	resolutionResetGPUUnconfirmed = "RESET_GPU_UNCONFIRMED"

	// resolutionUndecoded marks an NVLink5 event whose log line carried
	// no fields to decode, such as one in a format the sensor does not
	// parse. The fault still happened, so it is counted rather than
	// dropped.
	resolutionUndecoded = "UNDECODED"

	// resolutionXid154Eval is the table's deferral, not an action: the
	// row says the action is whatever Xid 154 reports for the same GPU.
	// An occurrence keeps this bucket when its GPU logged an Xid 154,
	// whose recovery action gpu_xid_154 reports.
	resolutionXid154Eval = "XID_154_EVAL"

	// resolutionXid154Missing marks a deferral with no Xid 154 logged for
	// its GPU. The driver logs a 154 only when its recovery action
	// changes, so this normally means it requested none.
	resolutionXid154Missing = "XID_154_MISSING"
)

// resolveNVLink5Event returns the immediate-action bucket for one
// NVLink5 event. logged154 reports whether its GPU logged an Xid 154,
// which decides whether a deferral has anything to defer to.
func resolveNVLink5Event(code int64, era driverEra, detail NVLink5Detail, logged154 bool) string {
	decoded := decodeNVLink5Resolution(code, era, detail)
	if decoded.Resolution == resolutionXid154Eval && !logged154 {
		return resolutionXid154Missing
	}

	if !decoded.Matched && decoded.Resolution == resolutionResetGPU {
		return resolutionResetGPUUnconfirmed
	}
	return decoded.Resolution
}

// nvlink5ResolutionKey is the nvlink5_resolution_counts key for one Xid
// code resolving to one bucket, e.g. "145.IGNORE".
//
// Keyed per code, not per bucket alone, so a per-code check can ask what
// its own code resolved to. A bucket-only key cannot answer that, which
// would leave every Xid 144-150 check firing on the bare occurrence and
// make the table's IGNORE rows unreachable.
func nvlink5ResolutionKey(code int64, bucket string) string {
	return fmt.Sprintf("%d.%s", code, bucket)
}

// nvlink5ResolutionCounts returns how many NVLink5 events in the window
// resolved to each immediate-action bucket, keyed by
// [nvlink5ResolutionKey].
//
// A row resolving to XID_154_EVAL names no action by itself: the action
// is whatever the Xid 154 logged for that same GPU requested, and
// gpu_xid_154 reports it. Here it only matters whether the GPU logged
// one, readable or not. Attribution is by PCI address, not proximity in
// the log: captured logs show two GPUs reporting the same Xid 145
// subtype in one window with only one of them logging an Xid 154.
//
// driverVersion is the host's driver version (see [health.Host]),
// classified into the era that decides which IntrInfo bit layout the
// table is read against; an unknown version limits matching to rows
// NVIDIA's table disambiguates by ErrorStatus alone rather than guessing
// a layout.
//
// Counts are per event, not per GPU: two faults on one GPU that both
// resolve to RESET_GPU count twice, because each is a separate
// occurrence to investigate.
func nvlink5ResolutionCounts(events []XidEvent, driverVersion health.DriverVersion) map[string]int64 {
	era := driverEraFrom(driverVersion)

	logged154 := make(map[string]bool)
	for _, ev := range events {
		if ev.XidCode == 154 {
			logged154[ev.PCIAddress] = true
		}
	}

	var counts map[string]int64
	for _, ev := range events {
		var bucket string
		switch {
		case ev.NVLink5 != nil:
			bucket = resolveNVLink5Event(ev.XidCode, era, *ev.NVLink5, logged154[ev.PCIAddress])
		case ev.XidCode >= 144 && ev.XidCode <= 150:
			bucket = resolutionUndecoded
		default:
			continue
		}
		if bucket == "" {
			continue
		}
		if counts == nil {
			counts = make(map[string]int64)
		}
		counts[nvlink5ResolutionKey(ev.XidCode, bucket)]++
	}
	return counts
}
