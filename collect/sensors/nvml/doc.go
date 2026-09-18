// Package nvml is the NVML-sourced GPU sensor: power and temperature readings per
// device. Single-source sensors group by their source. The NVML queries
// follow GPUd's power and temperature components. Power is converted from
// milliwatts to the canonical watt signals shared with DCGM.
//
// gpud's supported-flag sentinels become per-signal CollectionError
// observations carrying the native NVML return code: sensors normalize,
// they do not judge.
package nvml
