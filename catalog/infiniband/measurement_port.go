// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Port signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in checks that
// consume PortMeasurements. They are prefixed so they stay disjoint from
// the link-flap signals in the same category.
const (
	SignalIBDevice        = "ib_device"
	SignalIBPort          = "ib_port"
	SignalIBState         = "ib_state"
	SignalIBPhysicalState = "ib_physical_state"
	SignalIBRateGbps      = "ib_rate_gbps"
	SignalIBLinkLayer     = "ib_link_layer"
)

// PortMeasurementSignals lists every signal a PortMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot read the
// port at all, telling the catalog that no signal is trustworthy.
var PortMeasurementSignals = []string{
	SignalIBDevice,
	SignalIBPort,
	SignalIBState,
	SignalIBPhysicalState,
	SignalIBRateGbps,
	SignalIBLinkLayer,
}

// PortMeasurement is one port on one channel adapter, as reported by
// sysfs (/sys/class/infiniband). Sensors emit one per (adapter, port).
//
// State is the logical port state ("ACTIVE", "INIT", "ARMED", "DOWN") and
// PhysicalState the link-training state ("LinkUp", "Polling", "Disabled"),
// both as sysfs names them; a healthy port reports ACTIVE/LinkUp. RateGbps
// is the negotiated rate in Gb/s, a float because the slowest widths are
// fractional (2.5 for SDR 1x). LinkLayer is "InfiniBand" or, for RoCE
// ports, "Ethernet".
//
// Sensors call AddError for each field they could not read, naming the
// signals it would have populated. For a port that could not be observed at
// all, build the measurement with [ErroredPort].
type PortMeasurement struct {
	Device        string `json:"ib_device"`
	State         string `json:"ib_state"`
	PhysicalState string `json:"ib_physical_state"`
	LinkLayer     string `json:"ib_link_layer"`
	entity        health.EntityKey
	errors        []health.ErroredMeasurement
	Port          int64   `json:"ib_port"`
	RateGbps      float64 `json:"ib_rate_gbps"`
}

// ErroredPort returns a PortMeasurement marked errored for a port whose
// state could not be read. Without failedSignals every signal fails, so the
// checks report the collection failure rather than evaluating zero values.
func ErroredPort(device string, port int64, err error, failedSignals ...string) PortMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = PortMeasurementSignals
	}
	m := PortMeasurement{Device: device, Port: port}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement. err
// must be non-nil and is surfaced in the resulting verdict's message via
// its Error() string.
func (m *PortMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (PortMeasurement) Category() health.Category { return health.InfiniBand }

// EntityID implements [health.EntityIdentifier] as "<device>/<port>", e.g.
// "mlx5_0/1", so a failing per-port check names the port. A device-level
// failure (port 0) names the device alone, and an unknown device yields "",
// which the engine omits.
func (m PortMeasurement) EntityID() string {
	switch {
	case m.Device == "":
		return ""
	case m.Port == 0:
		return m.Device
	}
	return fmt.Sprintf("%s/%d", m.Device, m.Port)
}

// EntityKey implements [health.EntityKeyed]. Without a key from
// [PortMeasurement.SetEntityKey], a named port or device is verified under
// its EntityID and an unnamed one is unidentified.
func (m PortMeasurement) EntityKey() health.EntityKey {
	switch id := m.EntityID(); {
	case m.entity != health.EntityKey{}:
		return m.entity
	case id == "":
		return health.EntityKey{}
	default:
		return health.VerifiedEntity(id)
	}
}

// SetEntityKey records the identity evaluation keys this measurement by. Set
// it on a failure before any port was enumerated, which names no device: a
// [health.SourceEntity] of the producer, rather than an unidentified port.
func (m *PortMeasurement) SetEntityKey(k health.EntityKey) { m.entity = k }

// Errors implements [health.Measurement].
func (m PortMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
