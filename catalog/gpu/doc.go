// Package gpu defines the per-GPU measurement type ([GPUMeasurement])
// consumed by GPU-category health checks.
//
// The catalog ships no built-in GPU-category check definitions in this
// package today; XID-driven GPU checks live in [catalog/xid] and
// node-wide checks live in [catalog/node]. Import this package to
// construct or inspect GPU measurements that the sensors produce.
package gpu
