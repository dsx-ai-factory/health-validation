// Package xid defines the measurement type for NVRM Xid errors parsed
// from the system journal. Xid errors are GPU faults the NVIDIA driver
// logs to the kernel ring buffer / syslog; a sensor collects them and
// the catalog evaluates the aggregate against GPU checks.
package xid

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// Xid signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in checks
// that consume Xid measurements.
const (
	SignalEvents           = "events"
	SignalXidCounts        = "xid_counts"
	SignalFatalXidCounts   = "fatal_xid_counts"
	SignalSubXidCodeCounts = "sub_xid_code_counts"
	SignalDriverVersion    = "driver_version"
	SignalSince            = "since"
	SignalEventCount       = "event_count"
)

// MeasurementSignals lists every signal an XidMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot query
// the journal at all, telling the catalog that no signal is
// trustworthy for this measurement.
var MeasurementSignals = []string{
	SignalEvents,
	SignalXidCounts,
	SignalFatalXidCounts,
	SignalSubXidCodeCounts,
	SignalDriverVersion,
	SignalSince,
	SignalEventCount,
}

// XidEvent is a single parsed "NVRM: Xid …" log line. Pid and
// ProcessName are populated only when the driver attributed the fault
// to a process; some Xid lines carry neither. NVLink5 is populated only
// for the NVLink5/NVSwitch fault family (Xid 144-150), which carries a
// sub-component and severity instead of pid/name.
type XidEvent struct {
	NVLink5     *NVLink5Detail `json:"nvlink5,omitempty"`
	ProcessName string         `json:"process_name"`
	PCIAddress  string         `json:"pci_address"`
	XidCode     int64          `json:"xid_code"`
	Pid         int64          `json:"pid"`
}

// NVLink5Detail carries the extended fields NVRM logs for the
// NVLink5/NVSwitch fault family (Xid 144-150, Blackwell-only) instead
// of the pid/name pair other Xid codes use.
//
// Every field here is directly available from the log line: IntrInfo,
// ErrorStatus, and DebugData are the raw hex strings NVRM prints; Fatal
// and SubComponent are literal text in the log line (not
// register-decoded); SubXidCode is a fixed 6-bit mask over IntrInfo
// (see toolkit/xid/nvlink5_decode.go), not a table lookup. Not to be
// confused with NVIDIA's decode-table guide's "Subcode," which is a
// bit-encoding of SubComponent (redundant with the text NVRM already
// prints — see NVIDIA's Customer User Guide tab). Deciding what a given
// (code, SubComponent, ErrorStatus/IntrInfo) combination means —
// NVIDIA's documented resolution/investigation guidance — requires
// NVIDIA's Xid 144-150 decode table and the host's driver era, which is
// catalog knowledge: see [DecodeNVLink5Resolution].
type NVLink5Detail struct {
	SubComponent string `json:"sub_component"`
	IntrInfo     string `json:"intr_info,omitempty"`
	ErrorStatus  string `json:"error_status,omitempty"`
	// SubXidCode is "<XidCode>.<hex digit>" (e.g. "149.4", "149.A"),
	// identifying the specific fault subtype within this Xid code: the
	// 6-bit field at IntrInfo[25:20]. This bit position is constant
	// across driver eras (see toolkit/xid/nvlink5_decode.go). Empty
	// when IntrInfo could not be parsed.
	SubXidCode string   `json:"sub_xid_code,omitempty"`
	DebugData  []string `json:"debug_data,omitempty"`
	Link       int64    `json:"link"`
	Fatal      bool     `json:"fatal"`
}

// XidMeasurement is the aggregate of every Xid event observed in the
// window described by Since. One measurement is produced per sensor
// pass, even when no events were found (EventCount == 0), so a check
// always runs and can report PASS.
//
// XidCounts maps each Xid code to the number of times it occurred and
// is the headline signal for catalog rules (e.g. xid_counts[79] > 0).
// FatalXidCounts is the same shape, but counts only occurrences NVRM
// itself marked Fatal; today that is populated exclusively by the
// NVLink5 family (Xid 144-150) since no other Xid code's log line
// carries a fatal/nonfatal distinction. SubXidCodeCounts is keyed by
// [NVLink5Detail.SubXidCode] (e.g. "149.4") instead of the bare Xid
// code, letting a check threshold on a specific documented fault
// subtype rather than the whole Xid code (e.g.
// sub_xid_code_counts["149.4"] > xid_149_4_threshold). Populated only
// for the NVLink5 family, same as FatalXidCounts. DriverVersion is the
// raw host driver version string (e.g. "575.51.03", empty if
// undetectable) needed to classify [DriverEra] for
// [DecodeNVLink5Resolution]; a single host fact for the whole
// measurement, not per-event. Events preserves the full per-occurrence
// detail — process name, PCI address, pid, and (for 144-150) NVLink5 —
// for message interpolation and richer rules.
type XidMeasurement struct {
	XidCounts        map[int64]int64  `json:"xid_counts"`
	FatalXidCounts   map[int64]int64  `json:"fatal_xid_counts"`
	SubXidCodeCounts map[string]int64 `json:"sub_xid_code_counts"`
	Since            string           `json:"since"`
	DriverVersion    string           `json:"driver_version,omitempty"`
	Events           []XidEvent       `json:"events"`
	errors           []health.ErroredMeasurement
	EventCount       int64 `json:"event_count"`
}

// ErroredXid returns an XidMeasurement marked errored for a whole-query
// failure (the journal could not be read at all). Pass
// [MeasurementSignals]… for failedSignals so every Xid check that
// applies fails Critical rather than running CEL against an empty
// measurement. since records the window that was attempted.
func ErroredXid(since string, err error, failedSignals ...string) XidMeasurement {
	m := XidMeasurement{Since: since}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
// err must be non-nil and is surfaced in the resulting verdict's
// message via its Error() string.
func (m *XidMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (XidMeasurement) Category() health.Category { return health.Xid }

// Errors implements [health.Measurement].
func (m XidMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
