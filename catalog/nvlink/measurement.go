// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// NVLink signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in NVLink
// check definitions.
const (
	SignalGPUID                         = "gpu_id"
	SignalFabricBandwidthDegraded       = "fabric_bandwidth_degraded"
	SignalFabricRouteRecoveryInProgress = "fabric_route_recovery_in_progress"
	SignalFabricRouteUnhealthy          = "fabric_route_unhealthy"
	SignalFabricAccessTimeoutRecovery   = "fabric_access_timeout_recovery"
	SignalFabricRegistrationIncomplete  = "fabric_registration_incomplete"
	SignalFabricRegistrationFailed      = "fabric_registration_failed"
	SignalLinkCount                     = "link_count"
	SignalSpeedMBytesPerSec             = "speed_mbytes_per_sec"
	SignalInactiveCount                 = "inactive_count"
	SignalAllLinksSupportP2P            = "all_links_support_p2p"
	SignalAllLinksSupportSysmemAccess   = "all_links_support_sysmem_access"
	SignalAllLinksSupportP2PAtomics     = "all_links_support_p2p_atomics"
	SignalAllLinksSupportSysmemAtomics  = "all_links_support_sysmem_atomics"
	SignalAllLinksSupportSLI            = "all_links_support_sli"
	SignalAllLinksSupportLink           = "all_links_support_link"
	SignalUnhealthyP2PPeerCount         = "unhealthy_p2p_peer_count"
)

var measurementSignals = []string{
	SignalGPUID,
	SignalFabricBandwidthDegraded,
	SignalFabricRouteRecoveryInProgress,
	SignalFabricRouteUnhealthy,
	SignalFabricAccessTimeoutRecovery,
	SignalFabricRegistrationIncomplete,
	SignalFabricRegistrationFailed,
	SignalLinkCount,
	SignalSpeedMBytesPerSec,
	SignalInactiveCount,
	SignalAllLinksSupportP2P,
	SignalAllLinksSupportSysmemAccess,
	SignalAllLinksSupportP2PAtomics,
	SignalAllLinksSupportSysmemAtomics,
	SignalAllLinksSupportSLI,
	SignalAllLinksSupportLink,
	SignalUnhealthyP2PPeerCount,
}

// MeasurementSignals lists every signal an NVLinkMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot
// observe the device at all (e.g. a device handle that failed to load),
//
//	telling the catalog that no signal is trustworthy for this entity.
//
// The returned slice is a copy.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// NVLinkMeasurement captures NVLink interconnect signals for a single
// GPU. Sensors populate it from their telemetry source (NVML,
// nvidia-smi, …) and emit one per GPU per collection pass.
//
// Sensors call AddError for each failing query, naming the signals that
// query would have populated, so the catalog can decide per check
// whether to fail or run CEL against the partial data. For whole-device
// failures, build the measurement with [ErroredNVLink].
type NVLinkMeasurement struct {
	GPUID                         string `json:"gpu_id"`
	entity                        health.EntityKey
	errors                        []health.ErroredMeasurement
	unsupported                   []string
	LinkCount                     int64 `json:"link_count"`
	SpeedMBytesPerSec             int64 `json:"speed_mbytes_per_sec"`
	InactiveCount                 int64 `json:"inactive_count"`
	UnhealthyP2PPeerCount         int64 `json:"unhealthy_p2p_peer_count"`
	FabricBandwidthDegraded       bool  `json:"fabric_bandwidth_degraded"`
	FabricRouteRecoveryInProgress bool  `json:"fabric_route_recovery_in_progress"`
	FabricRouteUnhealthy          bool  `json:"fabric_route_unhealthy"`
	FabricAccessTimeoutRecovery   bool  `json:"fabric_access_timeout_recovery"`
	FabricRegistrationIncomplete  bool  `json:"fabric_registration_incomplete"`
	FabricRegistrationFailed      bool  `json:"fabric_registration_failed"`
	AllLinksSupportP2P            bool  `json:"all_links_support_p2p"`
	AllLinksSupportSysmemAccess   bool  `json:"all_links_support_sysmem_access"`
	AllLinksSupportP2PAtomics     bool  `json:"all_links_support_p2p_atomics"`
	AllLinksSupportSysmemAtomics  bool  `json:"all_links_support_sysmem_atomics"`
	AllLinksSupportSLI            bool  `json:"all_links_support_sli"`
	AllLinksSupportLink           bool  `json:"all_links_support_link"`
}

// ErroredNVLink returns an NVLinkMeasurement marked errored for a GPU
// whose NVLink state could not be collected. Pass [MeasurementSignals]…
// as failedSignals when the device is entirely unobservable — e.g. its
// handle failed to load, so even the GPU ID is unknown.
func ErroredNVLink(gpuID string, err error, failedSignals ...string) NVLinkMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	m := NVLinkMeasurement{GPUID: gpuID}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
func (m *NVLinkMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (NVLinkMeasurement) Category() health.Category { return health.NVLink }

// EntityID implements [health.EntityIdentifier].
func (m NVLinkMeasurement) EntityID() string { return m.GPUID }

// EntityKey implements [health.EntityKeyed]. Without a key from
// [NVLinkMeasurement.SetEntityKey], a non-empty GPUID is taken as a verified
// ID and an empty one as unidentified.
func (m NVLinkMeasurement) EntityKey() health.EntityKey {
	switch {
	case m.entity != health.EntityKey{}:
		return m.entity
	case m.GPUID == "":
		return health.EntityKey{}
	default:
		return health.VerifiedEntity(m.GPUID)
	}
}

// SetEntityKey records the identity evaluation keys this measurement by. Set
// it whenever GPUID is not a verified UUID, such as a slot whose UUID could
// not be read; GPUID then only labels the GPU in verdicts.
func (m *NVLinkMeasurement) SetEntityKey(k health.EntityKey) { m.entity = k }

// Errors implements [health.Measurement].
func (m NVLinkMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// MarkUnsupported records signals the source reported as unsupported.
// Their fields keep the value the sensor set, so evaluation is unchanged.
func (m *NVLinkMeasurement) MarkUnsupported(signals ...string) {
	m.unsupported = append(m.unsupported, signals...)
}

// UnsupportedSignals implements [health.UnsupportedSignaler].
func (m NVLinkMeasurement) UnsupportedSignals() []string { return m.unsupported }
