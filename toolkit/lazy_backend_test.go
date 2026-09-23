package toolkit_test

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// nodeMultiBackend is a combined multi-domain backend for the lazy-backend
// tests: it reports the given node measurement and empty GPU/NVLink results,
// satisfying [toolkit.MultiBackend] (the shape LazyGPUBackend constructs) so
// one value can back the Node and NVLink sensors at once. Close is a no-op.
type nodeMultiBackend struct {
	node []health.Measurement
}

func newNodeMultiBackend(m node.NodeMeasurement) nodeMultiBackend {
	return nodeMultiBackend{node: []health.Measurement{m}}
}

func (nodeMultiBackend) MeasureGPUs() ([]health.Measurement, error)   { return nil, nil }
func (b nodeMultiBackend) MeasureNode() ([]health.Measurement, error) { return b.node, nil }
func (nodeMultiBackend) MeasureNVLink() ([]health.Measurement, error) { return nil, nil }
func (nodeMultiBackend) Close() error                                 { return nil }

// TestLazyGPUBackend_HungInitDoesNotHang verifies a backend whose
// initialization blocks is bounded by the watchdog: the NVML-backed
// sensors degrade and the independent Xid sensor still runs, with no hang.
func TestLazyGPUBackend_HungInitDoesNotHang(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	release := make(chan struct{})
	defer close(release) // let the parked init goroutine exit after the test
	backend := toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) {
		<-release // model a wedged nvml.New()/lib.Init()
		return newNodeMultiBackend(node.NodeMeasurement{GPUCount: 4}), nil
	})
	// Node and NVLink share the backend; Xid is independent of it.
	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(backend),
		toolkit.NewNVLinkSensor(backend),
		toolkit.NewXidSensor(toolkit.MockXid()),
	}

	done := make(chan health.NodeVerdict, 1)
	go func() {
		nv, _ := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, sensors...)
		done <- nv
	}()

	select {
	case nv := <-done:
		if got := nv.SeverityByDomain[health.Node]; got != health.Critical {
			t.Errorf("node domain = %v, want CRITICAL (init timed out)", got)
		}
		if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
			t.Errorf("nvlink domain = %v, want CRITICAL (skipped after shared init wedged)", got)
		}
		if got := nv.SeverityByDomain[health.Xid]; got != health.Pass {
			t.Errorf("xid domain = %v, want PASS (independent of NVML init)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithOptions hung on a backend whose init blocks")
	}
}

// TestLazyGPUBackend_InitErrorDegrades verifies a backend whose
// initialization fails degrades the NVML-backed sensors to Critical
// without aborting the run, while the Xid sensor still produces a verdict.
func TestLazyGPUBackend_InitErrorDegrades(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	backend := toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) {
		return nil, errors.New("nvml init: ERROR_DRIVER_NOT_LOADED")
	})
	sensors := []toolkit.Sensor{
		toolkit.NewNodeSensor(backend),
		toolkit.NewNVLinkSensor(backend),
		toolkit.NewXidSensor(toolkit.MockXid()),
	}

	nv, err := toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: time.Second}, sensors...)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error, want graceful degrade: %v", err)
	}
	if got := nv.SeverityByDomain[health.Node]; got != health.Critical {
		t.Errorf("node domain = %v, want CRITICAL (init failed)", got)
	}
	if got := nv.SeverityByDomain[health.NVLink]; got != health.Critical {
		t.Errorf("nvlink domain = %v, want CRITICAL (init failed)", got)
	}
	if got := nv.SeverityByDomain[health.Xid]; got != health.Pass {
		t.Errorf("xid domain = %v, want PASS (independent of NVML init)", got)
	}
}

// closeSpyBackend's Measure blocks until release is closed, and Close
// records whether it was called — to verify the lazy backend's Close
// skips shutdown while a (timed-out) Measure is still in flight.
type closeSpyBackend struct {
	closed  *atomic.Bool
	release chan struct{}
}

func (s closeSpyBackend) MeasureGPUs() ([]health.Measurement, error)   { <-s.release; return nil, nil }
func (s closeSpyBackend) MeasureNode() ([]health.Measurement, error)   { <-s.release; return nil, nil }
func (s closeSpyBackend) MeasureNVLink() ([]health.Measurement, error) { <-s.release; return nil, nil }
func (s closeSpyBackend) Close() error                                 { s.closed.Store(true); return nil }

// TestLazyGPUBackend_SkipsCloseWhileMeasureInFlight verifies Close does NOT
// shut the backend down while a timed-out Measure goroutine is still parked
// inside it — shutting NVML down then could hang or crash.
func TestLazyGPUBackend_SkipsCloseWhileMeasureInFlight(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	var closed atomic.Bool
	release := make(chan struct{})
	defer close(release) // unpark the abandoned Measure goroutine after the test
	real := closeSpyBackend{closed: &closed, release: release}
	backend := toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) { return real, nil })

	// Time out MeasureNode so its goroutine stays parked inside the backend.
	done := make(chan struct{})
	go func() {
		_, _ = toolkit.CollectWithOptions(cat, toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, toolkit.NewNodeSensor(backend))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CollectWithOptions hung")
	}

	if err := backend.(io.Closer).Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if closed.Load() {
		t.Error("backend was shut down while a Measure was still in flight (could hang/crash on real NVML)")
	}
}

// TestLazyGPUBackend_ClosesWhenIdle verifies Close does shut the backend
// down once no Measure is in flight.
func TestLazyGPUBackend_ClosesWhenIdle(t *testing.T) {
	var closed atomic.Bool
	release := make(chan struct{})
	close(release) // Measure returns immediately
	real := closeSpyBackend{closed: &closed, release: release}
	backend := toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) { return real, nil })

	if _, err := backend.MeasureNode(); err != nil {
		t.Fatalf("MeasureNode: %v", err)
	}
	if err := backend.(io.Closer).Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if !closed.Load() {
		t.Error("backend was not shut down when no Measure was in flight")
	}
}
