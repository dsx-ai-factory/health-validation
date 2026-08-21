package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPU signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in GPU
// check definitions.
const (
	SignalGPUID               = "gpu_id"
	SignalGPUPstate           = "gpu_pstate"
	SignalAveragePowerDrawW   = "average_power_draw_w"
	SignalPowerLimitW         = "power_limit_w"
	SignalEnforcedPowerLimitW = "enforced_power_limit_w"
)

// MeasurementSignals lists every signal a GPUMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot
// observe the GPU at all (e.g. a device handle that failed to load),
// telling the catalog that no signal is trustworthy for this entity.
var MeasurementSignals = []string{
	SignalGPUID,
	SignalGPUPstate,
	SignalAveragePowerDrawW,
	SignalPowerLimitW,
	SignalEnforcedPowerLimitW,
}

// GPUMeasurement is a single observation of one GPU's health state.
// Partners populate this struct from their telemetry source (nvidia-smi,
// DCGM, NVML, or custom) and pass it to catalog evaluation.
//
//	m := gpu.GPUMeasurement{GPUID: "GPU-0"}
//	verdict, err := cat.EvaluateAll(m)
//
// Sensors that encounter per-field collection failures call AddError
// for each failing query, naming the signals that query would have
// populated. The catalog uses the accumulated error list to decide
// per check whether to fail Critical or run CEL against the partial
// data. For whole-entity failures (the sensor couldn't even identify
// the GPU), build the measurement with [ErroredGPU].
type GPUMeasurement struct {
	GPUID               string `json:"gpu_id"`
	errors              []health.ErroredMeasurement
	AveragePowerDrawW   float64 `json:"average_power_draw_w"`
	PowerLimitW         float64 `json:"power_limit_w"`
	EnforcedPowerLimitW float64 `json:"enforced_power_limit_w"`
	// GPUPstate is the GPU's current performance state as the numeric
	// level N in P<N>: 0 is P0 (maximum performance) and higher values
	// are progressively lower-power states.
	GPUPstate int64 `json:"gpu_pstate"`
}

// ErroredGPU returns a GPUMeasurement marked errored for a whole-GPU
// failure with one initial error entry. Pass [MeasurementSignals]…
// for failedSignals when the entire entity is unobservable. For
// partial failures, populate the struct directly and call
// [GPUMeasurement.AddError] for each failing query.
//
// gpuID identifies the affected GPU (e.g. a UUID or bus position) so
// downstream consumers can correlate the verdict with the underlying
// device. err describes why the measurement failed — sensors typically
// pass a domain-specific error type (e.g. nvml.Error) so consumers can
// type-assert for structured failure details.
func ErroredGPU(gpuID string, err error, failedSignals ...string) GPUMeasurement {
	m := GPUMeasurement{GPUID: gpuID}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
// Sensors call this for each failing query, listing the signals the
// failing call would have populated. Multiple AddError calls
// accumulate, so a single measurement can carry independent failures
// from different fields. err must be non-nil and is surfaced in the
// resulting verdict's message via its Error() string; pass a
// domain-specific error type (e.g. nvml.Error) so consumers can
// type-assert for structured failure details.
func (m *GPUMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (GPUMeasurement) Category() health.Category { return health.GPU }

// Errors implements [health.Measurement].
func (m GPUMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
