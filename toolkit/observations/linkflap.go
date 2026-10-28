// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// linkFlapSignals are the canonical RDMA link-flap signals. Like Xid, they do
// not fit the field table: one measurement aggregates every transition of
// the window, and outages exist only once downs are paired with ups.
var linkFlapSignals = func() map[string]bool {
	out := make(map[string]bool)
	for _, id := range ibsignals.LinkFlapSignals().IDs() {
		out[id] = true
	}
	return out
}()

// linkFlapSinceUnknown labels a failed measurement whose scan reported no
// window. Every window lies within the current boot, and the legacy sensor
// labels its failures the same way.
const linkFlapSinceUnknown = "boot"

// linkFlapTimeLayout renders journal times as the legacy reader's
// journalctl --output=short-full timestamps and --since argument: local
// time, second precision.
const linkFlapTimeLayout = time.DateTime

// canonicalLinkFlap builds one LinkFlapMeasurement from canonical link-flap
// observations. Any failure, a missing or repeated marker, or an incomplete
// or out-of-window transition yields a single error naming every
// MeasurementSignals entry, so a scan that lost one transition cannot pass.
// A single error, because evaluation recommends retry_collection only when
// every failure is a timeout.
//
// The observations need not have passed the bridge's validation, since
// [ToMeasurements] is public, so their shape is checked here too.
func canonicalLinkFlap(obs []*observation.Observation) health.Measurement {
	start, markerErr := linkFlapMarker(obs)
	since := linkFlapSinceUnknown
	if markerErr == nil {
		since = journalTime(start).Format(linkFlapTimeLayout)
	}
	if err := firstFailure(obs); err != nil {
		return infiniband.ErroredLinkFlap(since, err, infiniband.MeasurementSignals()...)
	}
	if markerErr != nil {
		return infiniband.ErroredLinkFlap(since, markerErr, infiniband.MeasurementSignals()...)
	}
	scan, scanErr := readLinkFlapScan(start, obs)
	if scanErr != nil {
		return infiniband.ErroredLinkFlap(since, scanErr, infiniband.MeasurementSignals()...)
	}
	m, err := buildLinkFlap(since, scan)
	if err != nil {
		return infiniband.ErroredLinkFlap(since, err, infiniband.MeasurementSignals()...)
	}
	return m
}

// linkFlapScan is a scan's marker and its transitions' values, keyed by
// journal entry in report order.
type linkFlapScan struct {
	entries map[string]map[string]*observation.Value
	order   []string
	start   int64
}

// linkFlapMarker returns the window start of the batch's one scan marker. A
// window that starts after the scan ran cannot have held its transitions.
func linkFlapMarker(obs []*observation.Observation) (int64, error) {
	var start int64
	markers := 0
	for _, o := range obs {
		if o.GetSignalId() != ibsignals.LinkFlapScanSince.ID() || o.GetValue() == nil {
			continue
		}
		markers++
		v, ok := o.GetValue().GetKind().(*observation.Value_IntValue)
		if !ok {
			return 0, errors.New("link-flap scan marker is not an integer")
		}
		if at := o.GetObservedAt(); at != nil && v.IntValue > at.AsTime().UnixMicro() {
			return 0, errors.New("link-flap scan window starts after the scan")
		}
		start = v.IntValue
	}
	switch {
	case markers == 0:
		return 0, errors.New("link-flap scan marker missing")
	case markers > 1:
		return 0, errors.New("link-flap scan marker observed more than once")
	}
	return start, nil
}

// readLinkFlapScan collects each entry's values. Outcomes other than values
// are firstFailure's to report and are skipped here.
func readLinkFlapScan(start int64, obs []*observation.Observation) (linkFlapScan, error) {
	scan := linkFlapScan{entries: map[string]map[string]*observation.Value{}, start: start}
	for _, o := range obs {
		if o.GetValue() == nil || o.GetSignalId() == ibsignals.LinkFlapScanSince.ID() {
			continue
		}
		entry := o.GetTarget().GetJournalEntry()
		if entry.GetBootId() == "" || entry.GetCursor() == "" {
			return linkFlapScan{}, fmt.Errorf("link transition signal %s has no journal entry target", o.GetSignalId())
		}
		key := entry.GetBootId() + "\x00" + entry.GetCursor()
		if scan.entries[key] == nil {
			scan.order = append(scan.order, key)
			scan.entries[key] = map[string]*observation.Value{}
		}
		if scan.entries[key][o.GetSignalId()] != nil {
			return linkFlapScan{}, fmt.Errorf("link transition %d: %s observed more than once", len(scan.order), o.GetSignalId())
		}
		scan.entries[key][o.GetSignalId()] = o.GetValue()
	}
	return scan, nil
}

// linkTransition is one transition, in the order the source reported it.
type linkTransition struct {
	at   time.Time
	down bool
}

// buildLinkFlap groups transitions by interface and pairs them. Per
// interface, in time order, each down pairs with the next up not yet
// paired:
//   - Consecutive downs before an up are one outage from the first down; the
//     later downs find no unpaired up and record nothing.
//   - A down with no later up in the window is an outage still in progress
//     and records nothing; the next scan whose window holds its up counts it.
//   - An up with no earlier down in the window recovers an outage that began
//     before it and records nothing.
//
// Times compare at second precision, in report order within a second, as
// the legacy reader's short-full timestamps did. A transition before the
// window start is a producer fault, not an older event to drop: the source
// claimed a window it did not keep.
func buildLinkFlap(since string, scan linkFlapScan) (infiniband.LinkFlapMeasurement, error) {
	byInterface := map[string][]linkTransition{}
	for i, key := range scan.order {
		f := scan.entries[key]
		iface, err := linkFlapString(f, ibsignals.LinkTransitionInterface, i+1)
		if err != nil {
			return infiniband.LinkFlapMeasurement{}, err
		}
		state, err := linkFlapString(f, ibsignals.LinkTransitionState, i+1)
		if err != nil {
			return infiniband.LinkFlapMeasurement{}, err
		}
		realtime, ok := f[ibsignals.LinkTransitionRealtime.ID()].GetKind().(*observation.Value_IntValue)
		if !ok {
			return infiniband.LinkFlapMeasurement{}, fmt.Errorf("link transition %d: missing integer %s", i+1, ibsignals.LinkTransitionRealtime.ID())
		}
		if !ibsignals.ValidInterfaceName(iface) || (state != ibsignals.LinkStateUp && state != ibsignals.LinkStateDown) {
			return infiniband.LinkFlapMeasurement{}, fmt.Errorf("link transition %d: invalid interface or state %q", i+1, state)
		}
		if realtime.IntValue < scan.start {
			return infiniband.LinkFlapMeasurement{}, fmt.Errorf("link transition %d: logged before the scan window", i+1)
		}
		byInterface[iface] = append(byInterface[iface], linkTransition{
			at:   journalTime(realtime.IntValue).Truncate(time.Second),
			down: state == ibsignals.LinkStateDown,
		})
	}

	m := infiniband.LinkFlapMeasurement{Since: since}
	for _, iface := range slices.Sorted(maps.Keys(byInterface)) {
		transitions := byInterface[iface]
		slices.SortStableFunc(transitions, func(a, b linkTransition) int { return a.at.Compare(b.at) })
		paired := make([]bool, len(transitions))
		for i, down := range transitions {
			if !down.down {
				continue
			}
			for j := i + 1; j < len(transitions); j++ {
				if transitions[j].down || paired[j] {
					continue
				}
				paired[j] = true
				up := transitions[j]
				seconds := int64(up.at.Sub(down.at) / time.Second)
				m.OutageDurations = append(m.OutageDurations, seconds)
				m.FlapEvents = append(m.FlapEvents, infiniband.LinkFlapEvent{
					Interface:       iface,
					DownAt:          down.at.Format(linkFlapTimeLayout),
					UpAt:            up.at.Format(linkFlapTimeLayout),
					DurationSeconds: seconds,
				})
				break
			}
		}
	}
	return m, nil
}

// journalTime converts a journal __REALTIME_TIMESTAMP to local time, the
// zone journalctl prints.
func journalTime(micros int64) time.Time { return time.UnixMicro(micros).In(time.Local) }

func linkFlapString(values map[string]*observation.Value, s collect.Signal, index int) (string, error) {
	v, ok := values[s.ID()].GetKind().(*observation.Value_StringValue)
	if !ok {
		return "", fmt.Errorf("link transition %d: missing string %s", index, s.ID())
	}
	return v.StringValue, nil
}
