package toolkit

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/NVIDIA/health-validation/catalog/health"
)

// MultiBackend is a single data source that covers every per-domain backend
// method — the shape of the shared host source (e.g. the NVML backend) that
// feeds the GPU, Node, and NVLink sensors from one device enumeration, plus
// Close for its lifecycle. [LazyGPUBackend] wraps one so its construction can
// be deferred under the collection watchdog.
type MultiBackend interface {
	MeasureGPUs() ([]health.Measurement, error)
	MeasureNode() ([]health.Measurement, error)
	MeasureNVLink() ([]health.Measurement, error)
	Close() error
}

// LazyGPUBackend wraps a backend constructor and defers it until the first
// Measure call, so an expensive or hang-prone initialization (NVML over
// cgo, for example) runs inside Collect's per-sensor timeout watchdog
// rather than before it. newFn is invoked at most once; a construction
// error or a hang then surfaces through the normal collection path — the
// dependent sensors degrade to errored measurements, or a hung init is
// bounded by the watchdog and trips the circuit breaker — instead of
// hanging or aborting the run before any verdict is produced.
//
// The returned value satisfies every narrow per-domain backend interface
// ([GPUBackend], [NodeBackend], [NVLinkBackend]) as well as [io.Closer], so
// one instance can be installed under all three sensors and shut down after
// the run. It also tracks in-flight Measure calls so Close can avoid
// shutting the backend down while a timed-out call may still be executing
// inside it (see [lazyGPUBackend.Close]).
func LazyGPUBackend(newFn func() (MultiBackend, error)) MultiBackend {
	return &lazyGPUBackend{newFn: newFn}
}

type lazyGPUBackend struct {
	newFn    func() (MultiBackend, error)
	backend  MultiBackend
	initErr  error
	inFlight atomic.Int64
	once     sync.Once
	ready    atomic.Bool
}

// ensure constructs the backend on first call and caches the result for
// subsequent calls (the GPU, Node, and NVLink sensors share one backend,
// so it is initialized once).
func (b *lazyGPUBackend) ensure() (MultiBackend, error) {
	b.once.Do(func() {
		b.backend, b.initErr = b.newFn()
		b.ready.Store(true)
	})
	return b.backend, b.initErr
}

// measure runs one collection through the backend, counting it as in
// flight for the call's duration (initialization included) so Close can
// tell whether a timed-out call may still be parked inside the backend.
func (b *lazyGPUBackend) measure(collect func(MultiBackend) ([]health.Measurement, error)) ([]health.Measurement, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)

	backend, err := b.ensure()
	if err != nil {
		return nil, err
	}
	return collect(backend)
}

func (b *lazyGPUBackend) MeasureGPUs() ([]health.Measurement, error) {
	return b.measure(MultiBackend.MeasureGPUs)
}

func (b *lazyGPUBackend) MeasureNode() ([]health.Measurement, error) {
	return b.measure(MultiBackend.MeasureNode)
}

func (b *lazyGPUBackend) MeasureNVLink() ([]health.Measurement, error) {
	return b.measure(MultiBackend.MeasureNVLink)
}

// Close shuts down the constructed backend, with two guards for the
// run-once CLI's timeout model:
//
//   - If a Measure call is still in flight, a timed-out collection may be
//     parked inside the backend (e.g. an uninterruptible NVML cgo call).
//     Shutting down now could hang or unload code still executing, so Close
//     skips cleanup and lets process exit reclaim the resources.
//   - Otherwise, if initialization never completed there is nothing to shut
//     down. ready is set only after the backend is stored, so Close never
//     blocks on init and never races its write of the backend.
func (b *lazyGPUBackend) Close() error {
	if b.inFlight.Load() > 0 {
		slog.Warn("skipping backend shutdown; a collection call is still in flight (likely timed out)")
		return nil
	}
	if !b.ready.Load() {
		return nil
	}
	if c, ok := b.backend.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
