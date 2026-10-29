// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

func TestToMeasurementsGPU(t *testing.T) {
	measurements := ToMeasurements(healthyGPUObservations())

	m := only[gpu.GPUMeasurement](t, measurements)
	if m.GPUID != "GPU-0" || m.AveragePowerDrawW != 75 || m.PowerLimitW != 700 ||
		m.EnforcedPowerLimitW != 700 || m.GPUPstate != 0 || !m.HWThermalSlowdown || m.HWSlowdown {
		t.Fatalf("unexpected measurement %+v", m)
	}
	if errs := mappedErrors(m); len(errs) != 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
}

func TestToMeasurementsKeepsPerSignalFailure(t *testing.T) {
	observations := replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(),
		failedObservation(gpusignals.PerformanceState.ID(), &observation.CollectionError{
			Source:    "dcgm",
			Operation: "metrics",
			Category:  observation.CollectionErrorCategoryTimeout,
			Detail:    "read timed out",
		}))

	byID := evaluate(t, "H100", ToMeasurements(observations), gpu.GPUPowerLimit(), gpu.GPUPowerState())
	if byID["gpu_power_limit"] != health.Pass || byID["gpu_power_state"] != health.Critical {
		t.Fatalf("unexpected severities %v", byID)
	}
	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if errs := mappedErrors(m); len(errs) != 1 || !errors.Is(errs[0].Err(), health.ErrCollectionTimeout) {
		t.Fatalf("want one timeout error, got %v", errs)
	}
}

func TestToMeasurementsLeavesReportedUnsupportedNeutral(t *testing.T) {
	for _, signal := range []string{
		gpusignals.HardwareSlowdownActive.ID(),
		gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(),
	} {
		t.Run(signal, func(t *testing.T) {
			observations := replace(append(healthyGPUObservations(), healthyNVLinkObservations()...), signal,
				noValueObservation(signal, observation.NoValueReasonNotSupported))

			measurements := ToMeasurements(observations)
			if errs := mappedErrors(only[gpu.GPUMeasurement](t, measurements)); len(errs) != 0 {
				t.Fatalf("unexpected GPU errors %v", errs)
			}
			if byID := evaluate(t, "GB200", measurements, nvlink.NVLinkFabricHealth()); byID["nvlink_fabric_health"] != health.Pass {
				t.Fatalf("unexpected severities %v", byID)
			}
		})
	}
}

// Only a NoValue reports a condition as unsupported; a collection error with
// the unsupported category is a failure to read the source.
func TestToMeasurementsFailsUnsupportedCollectionError(t *testing.T) {
	observations := replace(healthyNVLinkObservations(), gpusignals.NVLinkFabricRouteUnhealthy.ID(),
		failedObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), &observation.CollectionError{
			Category:    observation.CollectionErrorCategoryUnsupported,
			SourceError: "DCGM_FT_NOT_SUPPORTED",
		}))

	byID := evaluate(t, "GB200", ToMeasurements(observations), nvlink.NVLinkFabric(), nvlink.NVLinkFabricHealth())
	if byID["nvlink_fabric"] != health.Pass || byID["nvlink_fabric_health"] != health.Critical {
		t.Fatalf("unexpected severities %v", byID)
	}
}

func TestToMeasurementsFailsNoValueOutsideNeutralSignals(t *testing.T) {
	observations := replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(),
		noValueObservation(gpusignals.PerformanceState.ID(), observation.NoValueReasonNotSupported))

	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if got := failedSignals(m); !slices.Equal(got, []string{gpu.SignalGPUPstate}) {
		t.Fatalf("failed signals = %v", got)
	}
}

func TestToMeasurementsFailsNoTargets(t *testing.T) {
	noTargets := noValueObservation(gpusignals.PerformanceState.ID(), observation.NoValueReasonNoTargets)
	noTargets.Target = nil

	m := only[gpu.GPUMeasurement](t, ToMeasurements([]*observation.Observation{noTargets}))
	if m.GPUID != "gpu" || !slices.Contains(failedSignals(m), gpu.SignalGPUPstate) {
		t.Fatalf("unexpected measurement %+v, errors %v", m, m.Errors())
	}
}

func TestToMeasurementsGroupsBySourceLocalID(t *testing.T) {
	observations := healthyGPUObservations()
	for _, o := range observations {
		o.Target = observation.GPUSourceLocalTarget("3")
	}

	if m := only[gpu.GPUMeasurement](t, ToMeasurements(observations)); m.GPUID != `slot:"dcgm"/"3"` || len(mappedErrors(m)) != 0 {
		t.Fatalf("unexpected measurement %+v, errors %v", m, m.Errors())
	}
}

// The same slot from two sources is not known to be the same GPU, so each
// source's slot becomes its own measurement rather than one merged GPU.
func TestToMeasurementsKeepsSourceLocalSlotsPerSource(t *testing.T) {
	var observations []*observation.Observation
	for _, source := range []string{"dcgm", "nvml"} {
		for _, o := range healthyGPUObservations() {
			o.Source = source
			o.Target = observation.GPUSourceLocalTarget("0")
			observations = append(observations, o)
		}
	}

	var ids []string
	for _, m := range ToMeasurements(observations) {
		g := m.(gpu.GPUMeasurement)
		if errs := mappedErrors(g); len(errs) != 0 {
			t.Fatalf("%s: unexpected errors %v", g.GPUID, errs)
		}
		ids = append(ids, g.GPUID)
	}
	if !slices.Equal(ids, []string{`slot:"dcgm"/"0"`, `slot:"nvml"/"0"`}) {
		t.Fatalf("GPU IDs = %v, want one per source", ids)
	}
}

func TestToMeasurementsFailsSourceLocalSlotWithoutSource(t *testing.T) {
	observations := healthyGPUObservations()
	for _, o := range observations {
		o.Source = ""
		o.Target = observation.GPUSourceLocalTarget("0")
	}

	m := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if !slices.Contains(failedSignals(m), gpu.SignalGPUID) || !slices.Contains(failedSignals(m), gpu.SignalGPUPstate) {
		t.Fatalf("failed signals = %v, want the GPU identity and every observed signal", failedSignals(m))
	}
}

func TestToMeasurementsConvertsFabricManagerStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         int64
		wantIncomplete bool
		wantFailed     bool
		wantError      bool
	}{
		{name: "not supported", status: gpusignals.FabricManagerStatusNotSupported, wantIncomplete: true},
		{name: "not started", status: gpusignals.FabricManagerStatusNotStarted, wantIncomplete: true},
		{name: "in progress", status: gpusignals.FabricManagerStatusInProgress, wantIncomplete: true},
		{name: "success", status: gpusignals.FabricManagerStatusSuccess},
		{name: "failure", status: gpusignals.FabricManagerStatusFailure, wantFailed: true},
		{name: "unrecognized", status: gpusignals.FabricManagerStatusUnrecognized, wantError: true},
		{name: "NVML too old", status: gpusignals.FabricManagerStatusNVMLTooOld, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := replace(healthyNVLinkObservations(), gpusignals.FabricManagerStatus.ID(),
				valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(test.status)))

			m := only[nvlink.NVLinkMeasurement](t, ToMeasurements(observations))
			if m.FabricRegistrationIncomplete != test.wantIncomplete || m.FabricRegistrationFailed != test.wantFailed {
				t.Fatalf("unexpected registration %+v", m)
			}
			if got := failedSignals(m); slices.Contains(got, nvlink.SignalFabricRegistrationFailed) != test.wantError {
				t.Fatalf("registration failure recorded = %v, want %v", got, test.wantError)
			}
		})
	}
}

// Checks fed by signals with no mapped observation must not pass on the
// zero values of fields nobody collected.
func TestToMeasurementsFailsCatalogSignalsWithoutMappedFeed(t *testing.T) {
	byID := evaluate(t, "GB200", ToMeasurements(healthyNVLinkObservations()),
		nvlink.NVLinkFabricHealth(), nvlink.NVLinkActive())
	if byID["nvlink_fabric_health"] != health.Pass || byID["nvlink_active"] != health.Critical {
		t.Fatalf("unexpected severities %v", byID)
	}

	byID = evaluateIn(t, "H100", health.TenantGuest, ToMeasurements(healthyGPUObservations()), gpu.GPUComputeApps())
	if byID["gpu_compute_apps"] != health.Critical {
		t.Fatalf("unexpected severities %v", byID)
	}
}

// A domain with no observations yields no measurement, so evaluation skips
// its checks as missing, as run does when a sensor is absent.
func TestToMeasurementsWithNoObservations(t *testing.T) {
	if measurements := ToMeasurements(nil); len(measurements) != 0 {
		t.Fatalf("want no measurements, got %v", measurements)
	}
	byID := evaluate(t, "H100", ToMeasurements(healthyGPUObservations()), gpu.GPUPowerLimit(), nvlink.NVLinkFabricHealth())
	if _, evaluated := byID["nvlink_fabric_health"]; evaluated {
		t.Fatalf("nvlink_fabric_health evaluated without NVLink observations: %v", byID)
	}
}

func evaluate(t *testing.T, arch string, measurements []health.Measurement, checks ...health.CheckDefinition) map[string]health.Severity {
	t.Helper()
	return evaluateIn(t, arch, health.OperatorInfra, measurements, checks...)
}

func evaluateIn(
	t *testing.T,
	arch string,
	execution health.ExecutionContext,
	measurements []health.Measurement,
	checks ...health.CheckDefinition,
) map[string]health.Severity {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture(arch), catalog.WithExecutionContext(execution))
	for _, check := range checks {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err := cat.EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]health.Severity, len(verdict.CheckResults))
	for _, result := range verdict.CheckResults {
		byID[result.CheckID] = result.Severity
	}
	return byID
}

func only[M health.Measurement](t *testing.T, measurements []health.Measurement) M {
	t.Helper()
	var found []M
	for _, m := range measurements {
		if typed, ok := m.(M); ok {
			found = append(found, typed)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %T, got %d", *new(M), len(found))
	}
	return found[0]
}

// mappedErrors returns m's errors other than the entry naming catalog
// signals no mapped signal feeds, which depends on the catalog schema
// rather than on the observations under test.
func mappedErrors(m health.Measurement) []health.ErroredMeasurement {
	var errs []health.ErroredMeasurement
	for _, entry := range m.Errors() {
		if !errors.Is(entry.Err(), errNoObservation) {
			errs = append(errs, entry)
		}
	}
	return errs
}

func failedSignals(m health.Measurement) []string {
	var signals []string
	for _, entry := range mappedErrors(m) {
		signals = append(signals, entry.FailedSignals()...)
	}
	return signals
}

func missingSignals(m health.Measurement) []string {
	var signals []string
	for _, entry := range m.Errors() {
		if errors.Is(entry.Err(), errNoObservation) {
			signals = append(signals, entry.FailedSignals()...)
		}
	}
	return signals
}

func healthyGPUObservations() []*observation.Observation {
	return []*observation.Observation{
		valueObservation(gpusignals.PowerDraw.ID(), doubleValue(75)),
		valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700)),
		valueObservation(gpusignals.PowerLimitEnforced.ID(), doubleValue(700)),
		valueObservation(gpusignals.PerformanceState.ID(), intValue(0)),
		valueObservation(gpusignals.HardwareSlowdownActive.ID(), boolValue(false)),
		valueObservation(gpusignals.HardwareThermalSlowdownActive.ID(), boolValue(true)),
		valueObservation(gpusignals.HardwarePowerBrakeSlowdownActive.ID(), boolValue(false)),
	}
}

func healthyNVLinkObservations() []*observation.Observation {
	return []*observation.Observation{
		valueObservation(gpusignals.NVLinkFabricBandwidthDegraded.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricRouteRecoveryActive.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), boolValue(false)),
		valueObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), boolValue(false)),
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess)),
	}
}

func replace(observations []*observation.Observation, signalID string, with *observation.Observation) []*observation.Observation {
	for i, o := range observations {
		if o.GetSignalId() == signalID {
			observations[i] = with
		}
	}
	return observations
}

func valueObservation(signalID string, value *observation.Value) *observation.Observation {
	o := failedObservation(signalID, nil)
	o.Outcome = &observation.Observation_Value{Value: value}
	return o
}

func failedObservation(signalID string, failure *observation.CollectionError) *observation.Observation {
	var unit *string
	for _, s := range gpusignals.All() {
		if s.ID() == signalID {
			unit = s.Unit()
		}
	}
	return &observation.Observation{
		SignalId:   signalID,
		Source:     "dcgm",
		Target:     observation.GPUTarget("GPU-0"),
		ObservedAt: timestamppb.Now(),
		Unit:       unit,
		Outcome:    &observation.Observation_CollectionError{CollectionError: failure},
	}
}

func noValueObservation(signalID string, reason observation.NoValueReason) *observation.Observation {
	o := failedObservation(signalID, nil)
	o.Outcome = &observation.Observation_NoValue{NoValue: &observation.NoValue{Reason: reason}}
	return o
}

func TestMappedSignalsFor(t *testing.T) {
	gpuIDs := []string{
		gpusignals.PowerDraw.ID(),
		gpusignals.PowerLimitRequested.ID(),
		gpusignals.PowerLimitEnforced.ID(),
		gpusignals.PerformanceState.ID(),
		gpusignals.HardwareSlowdownActive.ID(),
		gpusignals.HardwareThermalSlowdownActive.ID(),
		gpusignals.HardwarePowerBrakeSlowdownActive.ID(),
	}
	nvlinkIDs := []string{
		gpusignals.NVLinkFabricBandwidthDegraded.ID(),
		gpusignals.NVLinkFabricRouteRecoveryActive.ID(),
		gpusignals.NVLinkFabricRouteUnhealthy.ID(),
		gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(),
		gpusignals.FabricManagerStatus.ID(),
	}
	if got := MappedSignalsFor(health.GPU); !slices.Equal(got, gpuIDs) {
		t.Fatalf("GPU signals = %v, want %v", got, gpuIDs)
	}
	if got := MappedSignalsFor(health.NVLink); !slices.Equal(got, nvlinkIDs) {
		t.Fatalf("NVLink signals = %v, want %v", got, nvlinkIDs)
	}
	cpuIDs := []string{
		cpusignals.PossibleCount.ID(),
		cpusignals.OnlineCount.ID(),
		cpusignals.NUMANodeOnlineCount.ID(),
	}
	if got := MappedSignalsFor(health.CPU); !slices.Equal(got, cpuIDs) {
		t.Fatalf("CPU signals = %v, want %v", got, cpuIDs)
	}
	if got := MappedSignalsFor(health.Node); !slices.Equal(got, []string{gpuinventory.GPUCount.ID()}) {
		t.Fatalf("Node signals = %v, want %v", got, gpuinventory.GPUCount.ID())
	}
	if got := MappedSignalsFor(health.Xid); !slices.Equal(got, xidsignals.All().IDs()) {
		t.Fatalf("Xid signals = %v, want %v", got, xidsignals.All().IDs())
	}
	// Xid fields build one measurement together, so each feeds every Xid
	// catalog signal and runnable-check narrowing keeps all or none.
	xidFeeds := MappedFeeds(health.Xid)
	if len(xidFeeds) != len(xidsignals.All()) {
		t.Fatalf("Xid feeds = %v, want one per Xid signal", xidFeeds)
	}
	for id, catalog := range xidFeeds {
		if !slices.Equal(catalog, xid.MeasurementSignals()) {
			t.Fatalf("Xid feed %s = %v, want %v", id, catalog, xid.MeasurementSignals())
		}
	}
	// Every mapped signal belongs to exactly one domain.
	perDomain := slices.Concat(MappedSignalsFor(health.CPU), MappedSignalsFor(health.Node), MappedSignalsFor(health.GPU), MappedSignalsFor(health.NVLink), MappedSignalsFor(health.Xid), MappedSignalsFor(health.PCIe))
	if !slices.Equal(perDomain, MappedSignals()) {
		t.Fatalf("per-domain signals %v do not partition %v", perDomain, MappedSignals())
	}
}

// TestToMeasurementsRejectsMixedXidForms fails Xid closed when one batch
// carries canonical and <domain>.<field> Xid observations.
func TestToMeasurementsRejectsMixedXidForms(t *testing.T) {
	marker := &observation.Observation{
		SignalId: xidsignals.ScanWindow.ID(), Source: "xid.journal", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: xidsignals.WindowBoot}}},
	}
	field := &observation.Observation{
		SignalId: "xid.event_count", Source: "toolkit.xid", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 0}}},
	}
	var xids []xid.XidMeasurement
	for _, m := range ToMeasurements([]*observation.Observation{marker, field}) {
		if typed, ok := m.(xid.XidMeasurement); ok {
			xids = append(xids, typed)
		}
	}
	if len(xids) != 1 || len(xids[0].Errors()) != 1 || !slices.Equal(xids[0].Errors()[0].FailedSignals(), xid.MeasurementSignals()) {
		t.Fatalf("mixed Xid forms = %+v, want one errored measurement", xids)
	}
}
