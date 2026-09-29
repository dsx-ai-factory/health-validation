package health

// Accumulator derives measurements from other measurements.
//
// A sensor reports what it observed. An accumulator reads everything a
// run collected and produces new measurements whose meaning depends on
// catalog knowledge or on more than one observation — a vendor decode
// table applied to logged events, or those events joined against a fact
// about the host that a different sensor reported. Its outputs are
// ordinary measurements: checks route to them by category and signal
// like any other, so a check reading a derived value needs nothing
// special.
//
// Accumulators are registered with the catalog and run inside every
// batch evaluation, so each source of measurements — the toolkit's
// sensors, or a consumer converting its own observations — gets the same
// derivations without running them itself.
type Accumulator interface {
	// Name identifies the accumulator in errors.
	Name() string

	// Outputs returns a zero value of every measurement type Accumulate
	// can produce. Coverage and signal-uniqueness checks reason about a
	// type's schema from a zero value; this lets them see a derived
	// measurement without running Accumulate.
	Outputs() []Measurement

	// Accumulate receives every measurement the run collected and returns
	// what it derives from them, or nothing when its inputs are absent.
	//
	// An input that failed to collect is the accumulator's to carry
	// forward: it must return its outputs errored for the signals that
	// depended on that input, or a check reading them would report PASS
	// on data nobody observed. The error return is reserved for a fault in
	// the accumulator itself, and fails the whole evaluation.
	Accumulate(measurements []Measurement) ([]Measurement, error)
}
