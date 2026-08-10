//go:build linux && !android

package app

import (
	"github.com/NVIDIA/health-validation/toolkit"
	"github.com/NVIDIA/health-validation/toolkit/nvml"
)

// nvmlHost is the production hostBackend: one [nvml.NVMLBackend] feeding the
// GPU, node, and NVLink sensors from a single device enumeration. The backend
// is wrapped in a [toolkit.LazyGPUBackend] so its NVML library open is
// deferred into Collect's per-sensor watchdog — a wedged driver or a missing
// libnvidia-ml then trips the watchdog and degrades the GPU-side sensors
// (while the independent Xid sensor still runs) instead of hanging before any
// verdict — and so close skips shutdown while a timed-out Measure may still be
// parked inside the cgo call.
//
// The one lazy backend is shared across all three per-domain accessors, so the
// GPU, Node, and NVLink sensors report the same resource key and the circuit
// breaker skips them together once it trips. b is the raw backend the probe
// accessor ([App.NVMLBackend]) hands out; lazy wraps it.
type nvmlHost struct {
	b    *nvml.NVMLBackend
	lazy toolkit.MultiBackend
}

func newHostBackend() hostBackend {
	b := nvml.New()
	return &nvmlHost{
		b:    b,
		lazy: toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) { return b, nil }),
	}
}

func (h *nvmlHost) gpuBackend() toolkit.GPUBackend       { return h.lazy }
func (h *nvmlHost) nodeBackend() toolkit.NodeBackend     { return h.lazy }
func (h *nvmlHost) nvlinkBackend() toolkit.NVLinkBackend { return h.lazy }
func (h *nvmlHost) architecture() (string, error)        { return h.b.Architecture() }
func (h *nvmlHost) close() error                         { return h.lazy.Close() }

// One NVMLBackend backs the GPU, node, and NVLink sensors: it implements each
// narrow per-domain interface (and MultiBackend for the lazy wrapper), so a
// single device enumeration feeds them all.
var (
	_ toolkit.GPUBackend    = (*nvml.NVMLBackend)(nil)
	_ toolkit.NodeBackend   = (*nvml.NVMLBackend)(nil)
	_ toolkit.NVLinkBackend = (*nvml.NVMLBackend)(nil)
	_ toolkit.MultiBackend  = (*nvml.NVMLBackend)(nil)
)

// NVMLBackend returns the App's built-in NVML backend. Layer custom per-GPU
// signals onto it with WithGPUProbe / WithNVLinkProbe and install the result
// via the matching setter:
//
//	a := app.New()
//	a.WithGPUBackend(a.NVMLBackend().WithGPUProbe(eccProbe{}))
//
// It is defined only on linux: the per-device probe type ([nvml.DeviceProber])
// is cgo/NVML-specific, and a consumer's probe code is linux-tagged too.
func (a *App) NVMLBackend() *nvml.NVMLBackend {
	return a.host.(*nvmlHost).b
}
