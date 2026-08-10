// Package toolkit provides the sensor and backend abstractions partners
// use to feed measurements into the catalog and produce a node verdict.
//
// A [Sensor] collects measurements from a hardware domain, either per observed
// entity or host-wide. [Collect] runs one or more sensors, hands their
// measurements to [catalog.Catalog.EvaluateAll], and returns the aggregated
// [health.NodeVerdict].
//
// # Sensors and reference backends
//
// Sensors wrap narrow, pluggable backend interfaces. The toolkit ships with
// production and test backends including:
//
//   - the toolkit/nvml backend, which collects GPU, node, and NVLink state;
//   - [SysfsCPU], which reads the Linux possible and online CPU lists and the
//     online NUMA node list, and emits one host-wide measurement;
//   - [Journalctl], which reads Xid events from the system journal; and
//   - [MockGPU], [MockCPU], and the other mock constructors used in tests.
//
// SysfsCPU reports the kernel's CPU availability and NUMA topology view, not
// expected VM/SKU capacity, process affinity, or cgroup CPU limits. Other backends (DCGM,
// nvidia-smi, partner-specific) are implemented out of tree by satisfying the
// relevant narrow interface, such as [GPUBackend] or [CPUBackend].
//
// # Wiring example
//
//	cat := catalog.New(
//	    catalog.WithArchitecture("GB200"),
//	    catalog.WithExecutionContext(health.OperatorInfra),
//	)
//	gpu := toolkit.NewGPUSensor(nvml.New())
//	cpu := toolkit.NewCPUSensor(toolkit.SysfsCPU())
//	nv, err := toolkit.Collect(cat, gpu, cpu)
//	if err != nil { /* handle */ }
//	os.Exit(nv.ExitCode())
package toolkit
