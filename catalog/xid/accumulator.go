// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"strconv"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/findings"
)

// Accumulator derives an [XidDerivedMeasurement] from each [XidMeasurement] a
// run collected: the counts, the subtypes, the events grouped by code, and
// which codes no check covers. It reads the registered checks for that last
// part, because whether a code is unknown depends on whether a check covers it.
// A check covers its codes whether or not it runs on this node: a code outside
// the architectures a check lists is not expected there.
//
// Deriving in the catalog rather than the sensor means every source of Xid
// events, whatever produced them, is interpreted the same way.
type Accumulator struct {
	// Claims maps the ID of a check of your own to the Xid codes it covers,
	// such as "179" or the NVLink5 subtype "149.4". A covered code is kept out
	// of gpu_xid_unknown and gpu_xid_informational, and the check's verdict
	// reports only its events. The checks [Checks] ships need no entry. An ID
	// that is not a registered check claims nothing. Register it with
	// [catalog.WithAccumulator].
	Claims map[string][]string
}

var _ health.Accumulator = Accumulator{}

// Name implements [health.Accumulator].
func (Accumulator) Name() string { return "xid" }

// Outputs implements [health.Accumulator].
func (Accumulator) Outputs() []health.Measurement {
	return []health.Measurement{XidDerivedMeasurement{}}
}

// Inputs implements [health.Accumulator].
func (Accumulator) Inputs() []health.Measurement {
	return []health.Measurement{XidMeasurement{}}
}

// Accumulate implements [health.Accumulator].
func (a Accumulator) Accumulate(measurements []health.Measurement, checks []*health.CheckDefinition) ([]health.Measurement, error) {
	claims := a.claimsFor(checks)
	var out []health.Measurement
	for _, m := range measurements {
		if x, ok := m.(XidMeasurement); ok {
			out = append(out, derive(x, claims))
		}
	}
	return out, nil
}

// claimsFor is the codes each Xid check in checks covers, by check ID.
func (a Accumulator) claimsFor(checks []*health.CheckDefinition) map[string][]string {
	claims := map[string][]string{}
	for _, def := range checks {
		if def.Category != health.Xid {
			continue
		}
		if codes, ok := a.Claims[def.ID]; ok {
			claims[def.ID] = codes
		} else if codes, ok := builtinClaims()[def.ID]; ok {
			claims[def.ID] = codes
		}
	}
	return claims
}

// derive computes x's derived signals. claims is the codes each check covers.
func derive(x XidMeasurement, claims map[string][]string) XidDerivedMeasurement {
	// Every derived signal is read from the events. If they could not be
	// collected, report the derived signals failed rather than empty: an
	// empty count reads as "no fault" and the checks would report PASS.
	for _, e := range x.Errors() {
		for _, sig := range e.FailedSignals() {
			if sig == SignalEvents {
				var d XidDerivedMeasurement
				d.errors = append(d.errors, health.NewErroredMeasurement(e.Err(), DerivedMeasurementSignals()...))
				return d
			}
		}
	}

	// The count maps are never nil, so a rule reads an empty one as "none".
	d := XidDerivedMeasurement{
		XidCounts:              map[int64]int64{},
		FatalXidCounts:         map[int64]int64{},
		UnknownXidCounts:       map[int64]int64{},
		InformationalXidCounts: map[int64]int64{},
		SubXidCodeCounts:       subXidCodeCounts(x.Events),
		Evidence:               make(map[string]findings.Evidence, len(claims)),
	}
	for _, e := range x.Events {
		d.XidCounts[e.XidCode]++
		if e.NVLink5 != nil && e.NVLink5.Fatal {
			d.FatalXidCounts[e.XidCode]++
		}
	}

	found := deriveFindings(x.Events)
	claimed := map[string]bool{}
	for id, codes := range claims {
		d.Evidence[id] = findings.Scope(found, codes)
		for _, code := range codes {
			claimed[code] = true
		}
	}
	for _, f := range found {
		if f.IsClaimed(claimed) {
			continue
		}
		code, _ := parseCode(f.Codes[0])
		if infoSet[code] {
			d.InformationalFindings = append(d.InformationalFindings, f)
			d.InformationalXidCounts[code] += f.Count
		} else {
			d.UnknownFindings = append(d.UnknownFindings, f)
			d.UnknownXidCounts[code] += f.Count
		}
	}
	return d
}

// eventCodes is every code an event answers to, least specific first: its Xid
// code, then for an NVLink5 fault the subtype a check can cover.
func eventCodes(e XidEvent) []string {
	code := strconv.FormatInt(e.XidCode, 10)
	if sub, ok := e.SubXidCode(); ok && hasSubXidCodeChecks(e.XidCode, e.NVLink5.SubComponent) {
		return []string{code, sub}
	}
	return []string{code}
}

// deriveFindings groups events by their most specific code, in the order each
// first appears. Within a finding, events with an identical driver line are
// one [EventGroup] with a count; an event with no line cannot be told apart
// from another, so it is never merged.
func deriveFindings(events []XidEvent) []findings.Finding {
	type group struct {
		byMessage map[string]int
		groups    []EventGroup
	}
	var out []findings.Finding
	var groups []*group
	index := map[string]int{}
	for _, e := range events {
		codes := eventCodes(e)
		key := codes[len(codes)-1]
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, findings.Finding{Codes: codes})
			groups = append(groups, &group{byMessage: map[string]int{}})
		}
		out[i].Count++
		g := groups[i]
		if j, ok := g.byMessage[e.Message]; ok && e.Message != "" {
			g.groups[j].Count++
			continue
		}
		g.byMessage[e.Message] = len(g.groups)
		g.groups = append(g.groups, EventGroup{XidEvent: e, Count: 1})
	}
	for i := range out {
		out[i].Evidence = groups[i].groups
	}
	return out
}
