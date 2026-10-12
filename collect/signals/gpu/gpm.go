package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

// GPM reports percentages, not DCGM profiling ratios.
var (
	GPMSMOccupancy           = collect.NewSignal("gpu.profile.sm.occupancy", "percent")
	GPMIntegerUtilization    = collect.NewSignal("gpu.profile.integer.utilization", "percent")
	GPMTensorUtilization     = collect.NewSignal("gpu.profile.tensor.utilization", "percent")
	GPMDFMATensorUtilization = collect.NewSignal("gpu.profile.tensor.dfma.utilization", "percent")
	GPMHMMATensorUtilization = collect.NewSignal("gpu.profile.tensor.hmma.utilization", "percent")
	GPMIMMATensorUtilization = collect.NewSignal("gpu.profile.tensor.imma.utilization", "percent")
	GPMFP64Utilization       = collect.NewSignal("gpu.profile.fp64.utilization", "percent")
	GPMFP32Utilization       = collect.NewSignal("gpu.profile.fp32.utilization", "percent")
	GPMFP16Utilization       = collect.NewSignal("gpu.profile.fp16.utilization", "percent")
)

func GPMSignals() collect.Signals {
	return collect.Signals{
		GPMSMOccupancy, GPMIntegerUtilization, GPMTensorUtilization,
		GPMDFMATensorUtilization, GPMHMMATensorUtilization, GPMIMMATensorUtilization,
		GPMFP64Utilization, GPMFP32Utilization, GPMFP16Utilization,
	}
}
