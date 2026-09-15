package health

// Measurement is the interface all domain measurement types implement.
//
// Each measurement carries data for one entity (e.g. one GPU) in one
// domain (e.g. GPU health). The catalog routes a measurement to a check
// when they share a Category and the measurement's schema carries every
// signal the check requires. Errors reports any per-signal collection failures
// the sensor encountered while building the measurement; the catalog
// fails a check whose RequiredSignals overlap with any error's
// FailedSignals and lets every other check run normally against the
// fields the sensor did populate.
type Measurement interface {
	// Category returns the hardware domain this measurement belongs to.
	Category() Category

	// Errors returns the list of per-signal collection failures the
	// sensor encountered, or nil/empty if the entity was fully
	// observable. Each entry names one or more signals that could
	// not be collected together with a human-readable detail; the
	// catalog inspects this list per check to decide whether to fail
	// or proceed with CEL evaluation against partial data.
	Errors() []ErroredMeasurement
}

// ErroredMeasurement records one per-signal collection failure on a
// measurement. Sensors append entries via the typed measurement's
// AddError method (e.g. [GPUMeasurement.AddError]) as they encounter
// failures while measuring different fields, so a single measurement
// may carry several entries — one per failing call.
//
// The catalog fails a check Critical when any entry's FailedSignals
// overlaps with the check's RequiredSignals; the verdict's message
// joins the matching entries' Err.Error() strings. Checks whose
// required signals were all collected still run normally on the
// partial data.
//
// The zero value is not valid; construct entries with
// [NewErroredMeasurement] (or via the typed measurement's AddError),
// which guarantees Err is non-nil so consumers always have a
// surfaceable failure cause.
type ErroredMeasurement struct {
	err           error
	failedSignals []string
}

// NewErroredMeasurement constructs an [ErroredMeasurement]. err must
// be non-nil — every entry carries a surfaceable failure cause that
// the catalog renders into the resulting CheckVerdict's Message.
// Passing nil is a programming error and panics.
func NewErroredMeasurement(err error, failedSignals ...string) ErroredMeasurement {
	if err == nil {
		panic("health: ErroredMeasurement requires non-nil Err")
	}
	return ErroredMeasurement{err: err, failedSignals: failedSignals}
}

// Err returns the underlying sensor error that prevented the listed
// signals from being collected. It is guaranteed non-nil for any
// ErroredMeasurement obtained through the public API. Its Error()
// string is what the catalog surfaces in the resulting CheckVerdict's
// Message; consumers that need structured details (e.g. an NVML
// Return code) can type-assert on the concrete error type the sensor
// produced.
func (m ErroredMeasurement) Err() error { return m.err }

// FailedSignals returns the signal names (matching the keys emitted
// into the signal map by the measurement's struct tags) that this
// entry's failure prevented the sensor from collecting. For
// whole-entity failures the entry includes every signal in the
// measurement schema; for per-field failures, only the specific
// signals the failing call would have populated.
func (m ErroredMeasurement) FailedSignals() []string { return m.failedSignals }

// DerivedSignaler is an optional interface a measurement may implement
// to expose signals it computes rather than stores.
//
// Signals are ordinarily the measurement struct's own json-tagged
// fields, which means a sensor has to compute anything a rule needs.
// That is the right split for facts a sensor can read off the wire, but
// not for values that require catalog knowledge — a vendor decode
// table, or a correlation across events that only means something in
// light of one. Implementing this interface keeps that reasoning in the
// catalog while still presenting the result to CEL as an ordinary
// signal.
//
// The returned map's *key set must not depend on the measurement's
// data*. Routing and the signal-uniqueness check ask a zero value which
// signals a type carries (see the evaluation package's SchemaKeys), so
// a key that appears only once some field is populated would be
// invisible to them, and a check requiring it would never be routed a
// measurement. Return every key every time, with an empty or nil value
// when there is nothing to report.
//
// Keys must also not collide with the type's own json field names. A
// derived signal takes precedence on collision, which silently shadows
// the field rather than failing.
type DerivedSignaler interface {
	DerivedSignals() map[string]any
}
