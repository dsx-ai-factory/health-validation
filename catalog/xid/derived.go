// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/findings"
)

// XidDerivedMeasurement is what the catalog reads out of an
// [XidMeasurement]'s events, as opposed to what a sensor collected. Only the
// [Accumulator] produces it, and every Xid check reads it.
//
// XidCounts maps each Xid code to the number of times it occurred and is the
// headline signal for catalog rules (e.g. xid_counts[79] >= 1). FatalXidCounts
// counts only occurrences NVRM itself marked Fatal, which today is the NVLink5
// family (Xid 144-150) alone. SubXidCodeCounts is keyed by
// [XidEvent.SubXidCode] (e.g. "149.4") so a check can act on one documented
// fault subtype.
//
// Evidence is what each check's verdict reports, keyed by check ID: the events
// for the codes that check covers, grouped by driver line. A check with no
// entry reports what it requires. The Unknown and Informational signals are the
// codes, and the findings, that no check covers: Informational for those
// NVIDIA's catalog gives no resolution, Unknown for the rest.
type XidDerivedMeasurement struct {
	XidCounts              map[int64]int64              `json:"xid_counts"`
	FatalXidCounts         map[int64]int64              `json:"fatal_xid_counts"`
	UnknownXidCounts       map[int64]int64              `json:"unknown_xid_counts"`
	InformationalXidCounts map[int64]int64              `json:"informational_xid_counts"`
	SubXidCodeCounts       map[string]int64             `json:"sub_xid_code_counts"`
	Evidence               map[string]findings.Evidence `json:"evidence"`
	UnknownFindings        []findings.Finding           `json:"unknown_findings"`
	InformationalFindings  []findings.Finding           `json:"informational_findings"`
	errors                 []health.ErroredMeasurement
}

// Category implements [health.Measurement].
func (XidDerivedMeasurement) Category() health.Category { return health.Xid }

// Errors implements [health.Measurement].
func (m XidDerivedMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
