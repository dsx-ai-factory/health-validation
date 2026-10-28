// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
)

// xidSignals are the canonical Xid signals. They do not fit the field table:
// one measurement aggregates repeated journal-entry events, and any failure
// must also fail the derived sub_xid_code_counts.
var xidSignals = func() map[string]bool {
	out := make(map[string]bool)
	for _, id := range xidsignals.All().IDs() {
		out[id] = true
	}
	return out
}()

// canonicalXid builds one XidMeasurement from canonical Xid observations.
// Any failure yields a single error naming every MeasurementSignals entry; a
// single error, because evaluation recommends retry_collection only when
// every failure is a timeout.
func canonicalXid(obs []*observation.Observation) health.Measurement {
	if err := firstXidFailure(obs); err != nil {
		return xid.ErroredXid(xidsignals.WindowBoot, err, xid.MeasurementSignals()...)
	}
	m, err := buildXid(obs)
	if err != nil {
		return xid.ErroredXid(xidsignals.WindowBoot, err, xid.MeasurementSignals()...)
	}
	return m
}

// firstXidFailure returns the first non-timeout failure, else the first
// timeout, else nil.
func firstXidFailure(obs []*observation.Observation) error {
	var timeout error
	for _, o := range obs {
		var err error
		switch {
		case o.GetCollectionError() != nil:
			err = collectionFailure(o.GetCollectionError())
		case o.GetNoValue() != nil:
			err = noValueFailure(o.GetSource(), o.GetNoValue())
		default:
			continue
		}
		if !errors.Is(err, health.ErrCollectionTimeout) {
			return err
		}
		if timeout == nil {
			timeout = err
		}
	}
	return timeout
}

func buildXid(obs []*observation.Observation) (xid.XidMeasurement, error) {
	var since string
	var marked bool
	var order []string
	entries := map[string]map[string]*observation.Value{}
	for _, o := range obs {
		if o.GetSignalId() == xidsignals.ScanWindow.ID() {
			_, marked = o.GetValue().GetKind().(*observation.Value_StringValue)
			since = o.GetValue().GetStringValue()
			continue
		}
		entry := o.GetTarget().GetJournalEntry()
		key := entry.GetBootId() + "\x00" + entry.GetCursor()
		if entries[key] == nil {
			order = append(order, key)
			entries[key] = map[string]*observation.Value{}
		}
		entries[key][o.GetSignalId()] = o.GetValue()
	}
	if !marked {
		return xid.XidMeasurement{}, errors.New("xid scan marker missing")
	}
	m := xid.XidMeasurement{Since: since}
	for i, key := range order {
		event, err := buildXidEvent(xidFields{values: entries[key], index: i + 1})
		if err != nil {
			return xid.XidMeasurement{}, err
		}
		if m.XidCounts == nil {
			m.XidCounts = map[int64]int64{}
		}
		m.XidCounts[event.XidCode]++
		if event.NVLink5 != nil && event.NVLink5.Fatal {
			if m.FatalXidCounts == nil {
				m.FatalXidCounts = map[int64]int64{}
			}
			m.FatalXidCounts[event.XidCode]++
		}
		m.Events = append(m.Events, event)
		m.EventCount++
	}
	return m, nil
}

// buildXidEvent builds one event. Recovery-action signals are read by no
// field: XidEvent does not carry the Xid 154 transition.
func buildXidEvent(f xidFields) (xid.XidEvent, error) {
	for _, required := range []collect.Signal{xidsignals.Code, xidsignals.PCIAddress} {
		if f.values[required.ID()] == nil {
			return xid.XidEvent{}, fmt.Errorf("xid event %d: missing %s", f.index, required.ID())
		}
	}
	event := xid.XidEvent{
		XidCode:     f.int(xidsignals.Code),
		PCIAddress:  f.string(xidsignals.PCIAddress),
		Pid:         f.int(xidsignals.PID),
		ProcessName: f.string(xidsignals.ProcessName),
	}
	nvlink5 := []collect.Signal{xidsignals.NVLink5SubComponent, xidsignals.NVLink5Fatal, xidsignals.NVLink5Link}
	present := slices.IndexFunc(nvlink5, func(s collect.Signal) bool { return f.values[s.ID()] == nil }) < 0
	if !present && slices.ContainsFunc(nvlink5, func(s collect.Signal) bool { return f.values[s.ID()] != nil }) {
		// Defaulting a missing Fatal to false would read a fatal fault as nonfatal.
		return xid.XidEvent{}, fmt.Errorf("xid event %d: incomplete NVLink5 fields", f.index)
	}
	if present {
		event.NVLink5 = &xid.NVLink5Detail{
			SubComponent: f.string(xidsignals.NVLink5SubComponent),
			Fatal:        f.bool(xidsignals.NVLink5Fatal),
			Link:         f.int(xidsignals.NVLink5Link),
			IntrInfo:     f.string(xidsignals.NVLink5IntrInfo),
			ErrorStatus:  f.string(xidsignals.NVLink5ErrorStatus),
		}
		if debug := strings.Fields(f.string(xidsignals.NVLink5DebugData)); len(debug) > 0 {
			event.NVLink5.DebugData = debug
		}
	}
	return event, f.err
}

// xidFields reads one event's values, recording the first value whose kind
// does not fit its signal; the bridge leaves kind checks to the consumer.
type xidFields struct {
	err    error
	values map[string]*observation.Value
	index  int
}

func (f *xidFields) wrongKind(s collect.Signal) {
	if f.err == nil {
		f.err = fmt.Errorf("xid event %d: signal %s has the wrong value kind", f.index, s.ID())
	}
}

func (f *xidFields) int(s collect.Signal) int64 {
	v := f.values[s.ID()]
	if v == nil {
		return 0
	}
	if _, ok := v.GetKind().(*observation.Value_IntValue); !ok {
		f.wrongKind(s)
	}
	return v.GetIntValue()
}

func (f *xidFields) string(s collect.Signal) string {
	v := f.values[s.ID()]
	if v == nil {
		return ""
	}
	if _, ok := v.GetKind().(*observation.Value_StringValue); !ok {
		f.wrongKind(s)
	}
	return v.GetStringValue()
}

func (f *xidFields) bool(s collect.Signal) bool {
	v := f.values[s.ID()]
	if v == nil {
		return false
	}
	if _, ok := v.GetKind().(*observation.Value_BoolValue); !ok {
		f.wrongKind(s)
	}
	return v.GetBoolValue()
}
