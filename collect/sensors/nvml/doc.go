// Package nvml is the NVML-sourced GPU sensor: temperature readings per
// device. Single-source sensors group by their source. The NVML queries
// follow gpud's temperature component: temperature.go mirrors
// components/accelerator/nvidia/temperature.
//
// gpud's supported-flag sentinels become per-signal CollectionError
// observations carrying the native NVML return code: sensors normalize,
// they do not judge.
package nvml
