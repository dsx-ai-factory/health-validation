// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"cmp"
	"fmt"
	"math"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// NVML values and collect NVML sensor outcomes the aggregation reads, so it
// can follow the legacy NVML backend's rules.
const (
	linkDisabled = 0 // nvmlEnableState_t FEATURE_DISABLED
	p2pOK        = 0 // nvmlGpuP2PStatus_t P2P_STATUS_OK

	nvmlNotSupported    = "ERROR_NOT_SUPPORTED"
	nvmlInvalidArgument = "ERROR_INVALID_ARGUMENT"
	opNvLinkState       = "GetNvLinkState"
)

// nvlinkLinks collects, per GPU UUID, the observations of aggregated mapping
// rows, which become catalog fields only together. Observations without a
// GPU UUID stay with the field table.
type nvlinkLinks map[string][]*observation.Observation

// add takes current if it belongs to an aggregated row and names a GPU UUID.
func (l nvlinkLinks) add(current *observation.Observation, f feed) bool {
	if !f.aggregated {
		return false
	}
	uuid := gpuRef(current.GetTarget()).GetUuid()
	if uuid == "" {
		return false
	}
	l[uuid] = append(l[uuid], current)
	return true
}

// feed writes each GPU's aggregated fields into b. A field none of whose
// signals was observed stays unfed, so it fails as missing.
func (l nvlinkLinks) feed(b *builder) {
	s := schemaFor(nvlink.NVLinkMeasurement{})
	for uuid, obs := range l {
		e := b.entity(s, uuid)
		bySignal := make(map[string][]*observation.Observation)
		for _, o := range obs {
			bySignal[o.GetSignalId()] = append(bySignal[o.GetSignalId()], o)
		}
		if states := bySignal[gpusignals.NVLinkState.ID()]; len(states) > 0 {
			count, err := inactiveCount(states)
			e.setAggregate(s, nvlink.SignalInactiveCount, count, err)
		}
		links, linksErr := gpuValue(bySignal[gpusignals.NVLinkLinkCount.ID()])
		if len(bySignal[gpusignals.NVLinkLinkCount.ID()]) > 0 {
			e.setAggregate(s, nvlink.SignalLinkCount, links, linksErr)
		}
		if common := bySignal[gpusignals.NVLinkSpeedCommon.ID()]; len(common) > 0 {
			speed, err := linkSpeed(common, bySignal[gpusignals.NVLinkEndpointSpeed.ID()], links, linksErr)
			e.setAggregate(s, nvlink.SignalSpeedMBytesPerSec, speed, err)
		}
		for _, capability := range gpusignals.EndpointCapabilitySignals() {
			if obs := bySignal[capability.ID()]; len(obs) > 0 {
				f, _ := resolve(obs[0])
				supported, err := allLinksSupport(obs)
				e.setAggregate(s, f.catalog[0], supported, err)
			}
		}
		if pairs := bySignal[gpusignals.NVLinkP2PStatus.ID()]; len(pairs) > 0 {
			count, err := unhealthyPeers(pairs)
			e.setAggregate(s, nvlink.SignalUnhealthyP2PPeerCount, count, err)
		}
	}
}

// setAggregate writes an aggregated catalog field, or the failure that
// prevents it.
func (e *entity) setAggregate(s *schema, field string, value any, err error) {
	if e.fed[field] {
		e.addError(fmt.Errorf("catalog signal %q was fed more than once", field), field)
		return
	}
	e.fed[field] = true
	if err != nil {
		e.addError(err, field)
		return
	}
	e.value.Field(s.fields[field]).Set(reflect.ValueOf(value))
}

// inactiveCount counts disabled links, following the legacy NVML reads:
// INVALID_ARGUMENT for a link is skipped, NOT_SUPPORTED ends the links (at
// link 0, reported for the whole GPU, it means no NVLink), and any other
// failure fails the count.
func inactiveCount(obs []*observation.Observation) (int64, error) {
	var count int64
	seen := make(map[uint32]bool)
	for _, o := range obs {
		endpoint := o.GetTarget().GetNvLinkEndpoint()
		if endpoint == nil {
			if o.GetCollectionError().GetSourceError() == nvmlNotSupported {
				continue
			}
			return 0, endpointFailure(o)
		}
		if seen[endpoint.GetLinkIndex()] {
			return 0, fmt.Errorf("signal %q was observed more than once for link %d", o.GetSignalId(), endpoint.GetLinkIndex())
		}
		seen[endpoint.GetLinkIndex()] = true
		if source := o.GetCollectionError().GetSourceError(); source == nvmlInvalidArgument || source == nvmlNotSupported {
			continue
		}
		state, ok := uintValue(o)
		if !ok {
			return 0, outcomeFailure(o)
		}
		if state == linkDisabled {
			count++
		}
	}
	return count, nil
}

// linkSpeed is the common NVLink speed. When the driver reports it
// unsupported, it falls back to the slowest per-link speed below the link
// count, and every one of those links must report a nonzero speed.
func linkSpeed(common, perLink []*observation.Observation, links int64, linksErr error) (int64, error) {
	speed, err := gpuValue(common)
	if err == nil || len(common) != 1 || common[0].GetCollectionError().GetSourceError() != nvmlNotSupported {
		return speed, err
	}
	if linksErr != nil {
		return 0, fmt.Errorf("per-link NVLink speed needs the link count: %w", linksErr)
	}
	speeds := make(map[int64]int64)
	for _, o := range perLink {
		endpoint := o.GetTarget().GetNvLinkEndpoint()
		if endpoint == nil {
			return 0, endpointFailure(o)
		}
		link := int64(endpoint.GetLinkIndex())
		if link >= links {
			continue
		}
		if _, repeated := speeds[link]; repeated {
			return 0, fmt.Errorf("signal %q was observed more than once for link %d", o.GetSignalId(), link)
		}
		value, ok := uintValue(o)
		if !ok {
			return 0, outcomeFailure(o)
		}
		speeds[link] = value
	}
	if links == 0 {
		return 0, fmt.Errorf("no NVLink reported a speed")
	}
	var slowest int64
	for link := range links {
		value, observed := speeds[link]
		switch {
		case !observed:
			return 0, fmt.Errorf("NVLink %d speed: %w", link, errNoObservation)
		case value == 0:
			return 0, fmt.Errorf("NVLink %d reported no speed", link)
		}
		if link == 0 || value < slowest {
			slowest = value
		}
	}
	return slowest, nil
}

// allLinksSupport ANDs one capability over a GPU's links in index order, as
// the legacy NVML reads did: the first link without it or failing to report
// it decides. The NVML sensor reports a failed state read on each capability
// of that link; INVALID_ARGUMENT there is skipped as legacy skips it, and a
// GPU with no present link supports the capability vacuously.
func allLinksSupport(obs []*observation.Observation) (bool, error) {
	slices.SortStableFunc(obs, func(a, b *observation.Observation) int {
		x, y := a.GetTarget().GetNvLinkEndpoint(), b.GetTarget().GetNvLinkEndpoint()
		if x == nil || y == nil {
			return cmp.Compare(boolInt(x == nil), boolInt(y == nil))
		}
		return cmp.Compare(x.GetLinkIndex(), y.GetLinkIndex())
	})
	seen := make(map[uint32]bool)
	for _, o := range obs {
		if endpoint := o.GetTarget().GetNvLinkEndpoint(); endpoint != nil {
			if seen[endpoint.GetLinkIndex()] {
				return false, fmt.Errorf("signal %q was observed more than once for link %d", o.GetSignalId(), endpoint.GetLinkIndex())
			}
			seen[endpoint.GetLinkIndex()] = true
			if failure := o.GetCollectionError(); failure.GetOperation() == opNvLinkState && failure.GetSourceError() == nvmlInvalidArgument {
				continue
			}
		}
		value, ok := uintValue(o)
		if !ok {
			return false, outcomeFailure(o)
		}
		if value == 0 {
			return false, nil
		}
	}
	return true, nil
}

// unhealthyPeers counts a GPU's peers whose P2P status is not OK; any failed
// pair fails the count. A lone GPU reports NO_TARGETS for itself, meaning it
// has no peers to count.
func unhealthyPeers(obs []*observation.Observation) (int64, error) {
	var count int64
	seen := make(map[string]bool)
	for _, o := range obs {
		pair := o.GetTarget().GetGpuPair()
		if pair == nil {
			if o.GetNoValue().GetReason() == observation.NoValueReasonNoTargets {
				continue
			}
			return 0, outcomeFailure(o)
		}
		peer := pair.GetPeerGpu().GetUuid()
		if seen[peer] {
			return 0, fmt.Errorf("signal %q was observed more than once for peer %q", o.GetSignalId(), peer)
		}
		seen[peer] = true
		status, ok := uintValue(o)
		if !ok {
			return 0, outcomeFailure(o)
		}
		if status != p2pOK {
			count++
		}
	}
	return count, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// gpuValue is the unsigned value of the single GPU-level observation in obs.
func gpuValue(obs []*observation.Observation) (int64, error) {
	switch {
	case len(obs) == 0:
		return 0, errNoObservation
	case len(obs) > 1:
		return 0, fmt.Errorf("signal %q was observed more than once", obs[0].GetSignalId())
	}
	value, ok := uintValue(obs[0])
	if !ok {
		return 0, outcomeFailure(obs[0])
	}
	return value, nil
}

func uintValue(o *observation.Observation) (int64, bool) {
	value, ok := o.GetValue().GetKind().(*observation.Value_UintValue)
	if !ok || value.UintValue > math.MaxInt64 {
		return 0, false
	}
	return int64(value.UintValue), true
}

// endpointFailure is the error a GPU-level observation of a per-link signal
// reports.
func endpointFailure(o *observation.Observation) error {
	if o.GetValue() != nil {
		return fmt.Errorf("signal %q has no NVLink endpoint target", o.GetSignalId())
	}
	return outcomeFailure(o)
}

// outcomeFailure is the error an observation without a usable value reports.
func outcomeFailure(o *observation.Observation) error {
	switch {
	case o.GetCollectionError() != nil:
		return collectionFailure(o.GetSource(), o.GetCollectionError())
	case o.GetNoValue() != nil:
		return noValueFailure(o.GetSource(), o.GetNoValue())
	case o.GetValue().GetUintValue() > math.MaxInt64:
		return fmt.Errorf("signal %q value %d does not fit an int64", o.GetSignalId(), o.GetValue().GetUintValue())
	default:
		return fmt.Errorf("signal %q value %v is not an unsigned integer", o.GetSignalId(), o.GetValue())
	}
}
