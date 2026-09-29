package toolkit

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// DefaultSensorTimeout bounds each sensor's collection when [Collect] is
// used. A sensor that does not return within this budget is degraded to
// errored measurements so a wedged data source fails the dependent checks
// instead of hanging the run.
const DefaultSensorTimeout = 60 * time.Second

// CollectionOptions configures one collection and evaluation run. Its zero
// value uses DefaultSensorTimeout and requests no cadence window.
type CollectionOptions struct {
	// Cadence selects the run window; nil evaluates every registered check.
	Cadence *health.SchedulingCadence
	// Timeout bounds each sensor; zero uses DefaultSensorTimeout.
	Timeout time.Duration
}

// Collect runs the provided sensors with the default per-sensor timeout,
// evaluates the catalog against the collected measurements, and
// aggregates results into a node-level verdict. It is a thin wrapper over
// [CollectWithOptions]; see it for collection and degradation semantics.
func Collect(cat *catalog.Catalog, sensors ...Sensor) (health.NodeVerdict, error) {
	return CollectWithOptions(cat, CollectionOptions{}, sensors...)
}

// CollectWithOptions is [Collect] with per-run collection and evaluation
// configuration. A zero timeout uses DefaultSensorTimeout; a nil cadence
// evaluates every registered check. Each sensor is bounded independently and runs sequentially:
// if a sensor's Measure does not return within timeout, or returns an
// error, the failure is degraded into errored measurements (via
// [Sensor.Errored]) so the catalog fails exactly the dependent checks
// Critical while every other sensor's checks still evaluate. A timed-out
// collection is tagged with [health.ErrCollectionTimeout] so evaluation
// can recommend a retry rather than hardware remediation.
//
// Sensors that share a backend (see [resourceKeyed]) are short-circuited:
// once one of them times out, the rest sharing that backend are skipped
// and degraded without re-entering the presumed-wedged data source.
// CollectWithOptions returns an error only when evaluation itself fails,
// never for a sensor failure.
func CollectWithOptions(cat *catalog.Catalog, opts CollectionOptions, sensors ...Sensor) (health.NodeVerdict, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultSensorTimeout
	}

	// Backend resource keys whose collection timed out; sensors sharing one
	// are skipped rather than re-entering a presumed-wedged data source.
	wedged := make(map[string]bool)

	var all []health.Measurement
	for _, s := range sensors {
		key := resourceKey(s)
		if key != "" && wedged[key] {
			// Degrade this sensor in its own name (not the wedging sensor's)
			// so the verdict points at the right domain.
			slog.Warn("skipping sensor; a sensor sharing its backend resource already timed out",
				"sensor", s.Name(), "resource", key)
			all = append(all, s.Errored(fmt.Errorf("%s sensor: shared backend resource timed out: %w",
				s.Name(), health.ErrCollectionTimeout))...)
			continue
		}

		r := collectSensor(s, timeout)
		switch {
		case r.timedOut:
			slog.Warn("sensor collection timed out; degrading to errored measurements",
				"sensor", s.Name(), "timeout", timeout)
			if key != "" {
				wedged[key] = true
			}
			all = append(all, s.Errored(fmt.Errorf("%s sensor: %w", s.Name(), health.ErrCollectionTimeout))...)
		case r.err != nil:
			slog.Warn("sensor collection failed; degrading to errored measurements",
				"sensor", s.Name(), "error", r.err)
			all = append(all, s.Errored(r.err)...)
		default:
			all = append(all, r.measurements...)
		}
	}

	nv, err := cat.EvaluateAllWithOptions(catalog.EvaluationOptions{Cadence: opts.Cadence}, all...)
	if err != nil {
		return nv, err
	}
	warnMissingSensors(nv)
	return nv, nil
}

// warnMissingSensors logs one warning per domain that reported nothing.
//
// The SENSOR_MISSING skips in the verdict are the machine-readable
// record; this is the operator-facing half, since a sensor absent from
// the run — never registered, backend never constructed, or returning no
// entities without an error — produces no other console output at all. A
// sensor that ran and failed is not this case: it is degraded above into
// errored measurements, so its category is present and its checks fail
// Critical with the cause attached.
func warnMissingSensors(nv health.NodeVerdict) {
	counts := make(map[health.Category]int)
	for _, v := range nv.SkippedChecks {
		if v.SkipReason == health.SensorMissing {
			counts[v.Category]++
		}
	}
	for _, cat := range slices.Sorted(maps.Keys(counts)) {
		slog.Warn("no measurement collected for domain; its checks were not assessed",
			"category", cat.String(), "skipped_checks", counts[cat])
	}
}

// resourceKeyed is implemented by sensors that share a data source with
// other sensors. CollectWithOptions uses the key to short-circuit: once a
// sensor with a given key times out, the rest sharing it are skipped
// (their source is presumed wedged) and degraded immediately instead of
// each waiting the full timeout. Sensors that don't implement it are
// always collected independently.
type resourceKeyed interface {
	ResourceKey() string
}

func resourceKey(s Sensor) string {
	if rk, ok := s.(resourceKeyed); ok {
		return rk.ResourceKey()
	}
	return ""
}

// sensorResult carries a sensor's raw Measure outcome from the collection
// goroutine back to the watchdog. timedOut reflects the watchdog's own
// deadline (the context), not the returned error — so a cancelled command
// that surfaces as "signal: killed" is still recognized as a timeout, and
// a sensor's unrelated internal deadline error is not.
type sensorResult struct {
	err          error
	measurements []health.Measurement
	timedOut     bool
}

// collectSensor runs one sensor's Measure under a timeout and returns its
// outcome.
//
// Measure runs on its own goroutine because a backend reading NVML over
// cgo cannot be cancelled mid-call: the context lets a cooperative backend
// stop early, but the select is what actually bounds the wait. On timeout
// the goroutine is abandoned — if its underlying call never returns it
// stays parked until the process exits, which is acceptable for a
// run-once tool and the only option for an uninterruptible call. The
// result channel is buffered so an abandoned goroutine can still deliver
// its result and exit instead of blocking forever on the send. A panic in
// Measure is recovered and degraded like any other failure so one bad
// backend cannot crash the whole run; a cgo segfault is not a Go panic and
// cannot be caught here.
func collectSensor(s Sensor, timeout time.Duration) sensorResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ch := make(chan sensorResult, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- sensorResult{err: fmt.Errorf("%s sensor panicked: %v", s.Name(), p)}
			}
		}()
		measurements, err := s.Measure(ctx)
		ch <- sensorResult{err: err, measurements: measurements}
	}()

	select {
	case r := <-ch:
		// Whether the sensor returned a value or an error, if our deadline
		// expired it was a collection timeout — authoritative regardless of
		// what the sensor reported (a killed command shows "signal: killed",
		// not a deadline error).
		if ctx.Err() != nil {
			r.timedOut = true
		}
		return r
	case <-ctx.Done():
		return sensorResult{timedOut: true}
	}
}
