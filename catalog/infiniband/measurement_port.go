package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

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
// ibstat. Sensors emit one per (adapter, port) per pass.
//
// State is the logical port state ("Active", "Init", "Armed", "Down") and
// PhysicalState the link-training state ("LinkUp", "Polling", "Disabled");
// a healthy port reports Active/LinkUp. RateGbps is the negotiated rate in
// Gb/s, a float because ibstat reports fractional rates for the slowest
// widths (2.5 for SDR 1x). LinkLayer is "InfiniBand" or, for RoCE ports,
// "Ethernet".
//
// Sensors call AddError for each field they could not read, naming the
// signals it would have populated. For a port that could not be observed at
// all, build the measurement with [ErroredPort].
type PortMeasurement struct {
	Device        string `json:"ib_device"`
	State         string `json:"ib_state"`
	PhysicalState string `json:"ib_physical_state"`
	LinkLayer     string `json:"ib_link_layer"`
	errors        []health.ErroredMeasurement
	Port          int64   `json:"ib_port"`
	RateGbps      float64 `json:"ib_rate_gbps"`
}

// ErroredPort returns a PortMeasurement marked errored for a port whose
// state could not be read. Pass [PortMeasurementSignals]… for failedSignals
// when nothing about the port is known, including its identity.
func ErroredPort(device string, port int64, err error, failedSignals ...string) PortMeasurement {
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

// Errors implements [health.Measurement].
func (m PortMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
