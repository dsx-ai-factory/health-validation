// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type fakeHealthSource struct {
	metricWatchError error
	healthWatchError error
	fakeCollectionSource
	metricWatch         []string
	healthWatch         uint64
	metricRegistrations int
	healthRegistrations int
}

func (source *fakeHealthSource) AddMetricsToWatch(ids []string) error {
	source.metricRegistrations++
	for _, id := range ids {
		if !slices.Contains(source.metricWatch, id) {
			source.metricWatch = append(source.metricWatch, id)
		}
	}
	return source.metricWatchError
}

func (source *fakeHealthSource) AddHealthToWatch(systems uint64) error {
	source.healthRegistrations++
	if source.healthWatchError != nil {
		return source.healthWatchError
	}
	source.healthWatch = systems
	return nil
}

func newFakeHealthSource() *fakeHealthSource {
	return &fakeHealthSource{fakeCollectionSource: fakeCollectionSource{result: dcgmsource.CollectionResult{
		GPUs: []dcgmsource.CollectedGPU{sampleGPUMetrics(dcgmsource.GPUIdentityEntry{Index: 7, UUID: "GPU-7"})},
		Inventory: &observation.GPUInventorySnapshot{
			Source:  dcgmsource.Name,
			Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{}},
		},
		HealthReport: &observation.DCGMHealthReport{
			GroupId:          proto.Uint64(40),
			RequestedSystems: dcgmhealth.KnownSystems,
			Entities: []*observation.ObservationTarget{
				{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{
					Uuid: "GPU-7", Native: &observation.NativeEntityRef{Source: dcgmsource.Name, EntityKind: 1, EntityId: 7},
				}}},
				{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{
					Native: &observation.NativeEntityRef{Source: dcgmsource.Name, EntityKind: 3, EntityId: 12},
				}}},
			},
			NativeGroupOverallHealth: observation.DCGMHealthResultFail.Enum(),
			MonitoredSince:           timestamppb.New(time.Unix(100, 0)),
			CollectionId:             "native-collection-1",
		},
	}}}
}

func rawHealthTarget(kind, id uint32) *observation.ObservationTarget {
	return &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{
		NativeEntity: &observation.NativeEntityRef{Source: dcgmsource.Name, EntityKind: kind, EntityId: id},
	}}
}

func measureHealthSensor(t *testing.T, sensor collect.Sensor, options ...collect.OpOption) *observation.ObservationBatch {
	t.Helper()
	cycle, err := collect.NewCycle("health-test", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	options = append(options, collect.WithCycle(cycle))
	batch, err := sensor.Measure(context.Background(), options...)
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

func configuredHealthSensor(t *testing.T, source *fakeHealthSource, metricSignalIDs ...string) collect.Sensor {
	t.Helper()
	sensor, err := newHealthSensor(source, dcgmhealth.KnownSystems, metricSignalIDs)
	if err != nil {
		t.Fatal(err)
	}
	return sensor
}

func TestHealthConstructorRegistration(t *testing.T) {
	for _, test := range []struct {
		name    string
		ids     []string
		metrics []string
		systems uint64
	}{
		{name: "disabled with no metrics"},
		{name: "disabled with explicit metrics", ids: []string{gpusignals.PowerDraw.ID()}, metrics: []string{gpusignals.PowerDraw.ID()}},
		{name: "health only default", systems: dcgmhealth.SystemNVSwitchFatal},
		{
			name: "explicit deduplicated metrics", systems: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe,
			ids:     []string{gpusignals.PowerDraw.ID(), gpusignals.TemperatureCelsius.ID(), gpusignals.PowerDraw.ID()},
			metrics: []string{gpusignals.PowerDraw.ID(), gpusignals.TemperatureCelsius.ID()},
		},
		{name: "all metrics explicitly requested", systems: dcgmhealth.KnownSystems, ids: supportedSignals.IDs(), metrics: supportedSignals.IDs()},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newFakeHealthSource()
			sensor, err := newHealthSensor(source, test.systems, test.ids)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(source.metricWatch, test.metrics) {
				t.Fatalf("registered metrics = %v, want %v", source.metricWatch, test.metrics)
			}
			wantMetricCalls, wantHealthCalls := 0, 0
			if len(test.metrics) > 0 {
				wantMetricCalls = 1
			}
			if source.healthWatch != test.systems {
				t.Fatalf("registered health mask = %#x, want %#x", source.healthWatch, test.systems)
			}
			if test.systems != 0 {
				wantHealthCalls = 1
			}
			if source.metricRegistrations != wantMetricCalls || source.healthRegistrations != wantHealthCalls {
				t.Fatalf("registration calls metrics=%d health=%d", source.metricRegistrations, source.healthRegistrations)
			}
			declared := sensor.Signals()
			wantSignals := slices.Clone(test.metrics)
			if test.systems != 0 {
				wantSignals = append(wantSignals, dcgmhealth.Report.ID())
			}
			if !slices.Equal(declared.IDs(), wantSignals) {
				t.Fatalf("declared signals = %v", declared.IDs())
			}
			if len(test.ids) > 0 && declared[0].ID() != test.ids[0] {
				t.Fatal("explicit signal order was lost")
			}
			if len(declared) > 0 {
				declared[0] = collect.NewSignal("mutated", "")
				if sensor.Signals()[0].ID() == "mutated" {
					t.Fatal("Signals exposed mutable sensor configuration")
				}
			}
		})
	}
}

func TestHealthConstructorRejectsInvalidBeforeRegistration(t *testing.T) {
	for _, invalid := range []string{dcgmhealth.Report.ID(), "dcgm.health.unknown", "dcgm.health.memory"} {
		for _, ids := range [][]string{{invalid}, {gpusignals.PowerDraw.ID(), invalid}} {
			for _, systems := range []uint64{0, dcgmhealth.KnownSystems} {
				source := newFakeHealthSource()
				_, err := newHealthSensor(source, systems, ids)
				if err == nil || source.metricRegistrations != 0 || source.healthRegistrations != 0 {
					t.Fatalf("validation error=%v registrations metrics=%d health=%d", err, source.metricRegistrations, source.healthRegistrations)
				}
			}
		}
	}
}

func TestDisabledHealthDoesNotRegisterOrRead(t *testing.T) {
	for _, sharedHealth := range []bool{false, true} {
		for _, metrics := range [][]string{nil, {gpusignals.PowerDraw.ID()}} {
			t.Run(fmt.Sprintf("shared=%t/metrics=%d", sharedHealth, len(metrics)), func(t *testing.T) {
				source := newFakeHealthSource()
				wantHealthRegistrations := 0
				if sharedHealth {
					if _, err := newHealthSensor(source, dcgmhealth.SystemMemory, nil); err != nil {
						t.Fatal(err)
					}
					wantHealthRegistrations = 1
				}
				source.healthWatchError = errors.New("disabled health must not register")
				source.result.HealthReport = nil
				source.result.HealthError = &observation.CollectionError{Category: observation.CollectionErrorCategorySourceError}
				disabled, err := newHealthSensor(source, 0, metrics)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(disabled.Signals().IDs(), metrics) || source.healthRegistrations != wantHealthRegistrations {
					t.Fatalf("disabled configuration: signals=%v health registrations=%d", disabled.Signals().IDs(), source.healthRegistrations)
				}
				batch := measureHealthSensor(t, disabled)
				if len(batch.Observations) != len(metrics) {
					t.Fatalf("disabled collection: observations=%v", batch.Observations)
				}
				var requests []dcgmsource.CollectionRequest
				if len(metrics) > 0 {
					requests = append(requests, dcgmsource.CollectionRequest{Metrics: metrics})
					if batch.Observations[0].GetValue().GetDoubleValue() != 100 {
						t.Fatalf("disabled health suppressed explicit metrics: %v", batch)
					}
				}
				batch = measureHealthSensor(t, disabled, collect.WithSignalID(), collect.WithGPUInventory())
				if len(batch.Observations) != 0 || len(batch.GetInventory().GetGpuSnapshots()) != 1 {
					t.Fatalf("inventory-only collection: %v", batch)
				}
				source.assertRequests(t, append(requests, dcgmsource.CollectionRequest{Inventory: true})...)
			})
		}
	}
}

func TestHealthConstructorRegistrationErrors(t *testing.T) {
	for _, feature := range []string{"metric", "health"} {
		source := newFakeHealthSource()
		if feature == "metric" {
			source.metricWatchError = errors.New("watch refused")
		} else {
			source.healthWatchError = errors.New("watch refused")
		}
		_, err := newHealthSensor(source, dcgmhealth.SystemMemory, []string{gpusignals.PowerDraw.ID()})
		if err == nil || !strings.Contains(err.Error(), "configure DCGM "+feature+" watch") {
			t.Fatalf("registration error = %v", err)
		}
	}
}

func TestInvalidHealthMaskDoesNotRegisterAbandonedMetrics(t *testing.T) {
	for _, selection := range []struct {
		name string
		ids  []string
	}{
		{name: "health only"},
		{name: "with metrics", ids: []string{gpusignals.PowerDraw.ID()}},
	} {
		for _, systems := range []uint64{1 << 63, dcgmhealth.SystemAll} {
			t.Run(fmt.Sprintf("%s/%#x", selection.name, systems), func(t *testing.T) {
				source := newFakeHealthSource()
				source.healthWatchError = errors.New("invalid health mask")
				if _, err := newHealthSensor(source, systems, selection.ids); err == nil {
					t.Fatal("invalid health mask was accepted")
				}
				if source.metricRegistrations != 0 || len(source.metricWatch) != 0 {
					t.Fatalf("failed construction registered metrics: calls=%d signals=%v", source.metricRegistrations, source.metricWatch)
				}
				source.healthWatchError = nil
				_, err := newHealthSensor(source, dcgmhealth.SystemMemory, []string{gpusignals.TemperatureCelsius.ID()})
				if err != nil {
					t.Fatal(err)
				}
				if source.metricRegistrations != 1 || !slices.Equal(source.metricWatch, []string{gpusignals.TemperatureCelsius.ID()}) {
					t.Fatalf("valid retry retained abandoned metrics: calls=%d signals=%v", source.metricRegistrations, source.metricWatch)
				}
			})
		}
	}
}

func TestNewWithHealthSourceModesAndMasks(t *testing.T) {
	for name, source := range map[string]*dcgmsource.Source{
		"standalone": dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{}),
		"embedded":   dcgmsource.NewEmbeddedSource(dcgmsource.EmbeddedOptions{}),
	} {
		healthOnly, err := NewWithHealth(source, dcgmhealth.SystemMemory)
		if err != nil {
			t.Fatalf("NewWithHealth(%s) health-only: %v", name, err)
		}
		if !slices.Equal(healthOnly.Signals().IDs(), []string{dcgmhealth.Report.ID()}) {
			t.Fatalf("NewWithHealth(%s) default signals = %v, want health only", name, healthOnly.Signals().IDs())
		}
		if _, err := NewWithHealth(source, dcgmhealth.SystemMemory, gpusignals.PowerDraw.ID()); err != nil {
			t.Fatalf("NewWithHealth(%s): %v", name, err)
		}
		for _, metrics := range [][]string{nil, {gpusignals.PowerDraw.ID()}} {
			disabled, err := NewWithHealth(source, 0, metrics...)
			if err != nil || !slices.Equal(disabled.Signals().IDs(), metrics) {
				t.Fatalf("NewWithHealth(%s, 0) error=%v sensor=%v", name, err, disabled)
			}
		}
	}
	for _, systems := range []uint64{1 << 63, dcgmhealth.SystemAll} {
		for _, metrics := range [][]string{nil, {gpusignals.PowerDraw.ID()}} {
			source := dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})
			if _, err := NewWithHealth(source, systems, metrics...); err == nil {
				t.Fatalf("NewWithHealth accepted invalid mask %#x", systems)
			}
		}
	}
	source := dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})
	if _, err := NewWithHealth(source, dcgmhealth.SystemMemory, dcgmhealth.Report.ID()); err == nil {
		t.Fatal("NewWithHealth accepted a report ID as a metric")
	}
	if _, err := New(source, dcgmhealth.Report.ID()); err == nil {
		t.Fatal("New accepted a report ID as a metric")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) succeeded")
	}
	if _, err := NewWithHealth(nil, dcgmhealth.KnownSystems); err == nil {
		t.Fatal("NewWithHealth(nil) succeeded")
	}
}

func TestMeasureRequestsMetricsHealthAndInventoryTogether(t *testing.T) {
	source := newFakeHealthSource()
	sensor := configuredHealthSensor(t, source, gpusignals.PowerDraw.ID(), gpusignals.PowerDraw.ID())
	batch := measureHealthSensor(t, sensor, collect.WithGPUInventory())
	source.assertRequests(t, dcgmsource.CollectionRequest{Metrics: []string{gpusignals.PowerDraw.ID()}, Health: true, Inventory: true})
	if len(batch.Observations) != 2 || len(batch.GetInventory().GetGpuSnapshots()) != 1 {
		t.Fatalf("metrics, health, and inventory collection: %v", batch)
	}
	if batch.Observations[0].GetValue().GetDoubleValue() != 100 || batch.Observations[0].GetSignalId() != gpusignals.PowerDraw.ID() {
		t.Fatalf("metric result = %v", batch.Observations[0])
	}
	current := batch.Observations[1]
	if _, ok := current.GetTarget().GetKind().(*observation.ObservationTarget_Node); !ok {
		t.Fatalf("health target = %v", current.GetTarget())
	}
	if current.GetSignalId() != dcgmhealth.Report.ID() || current.Unit != nil || !proto.Equal(current.GetValue().GetDcgmHealthReport(), source.result.HealthReport) {
		t.Fatalf("aggregate result = %v", current)
	}
}

func TestMetricsAndHealthUseSharedGPUReferences(t *testing.T) {
	source := newFakeHealthSource()
	batch := measureHealthSensor(t, configuredHealthSensor(t, source, gpusignals.PowerDraw.ID()))
	metricGPU := batch.Observations[0].GetTarget().GetGpu()
	healthGPU := batch.Observations[1].GetValue().GetDcgmHealthReport().GetEntities()[0].GetGpu()
	if metricGPU == nil || healthGPU == nil || metricGPU.GetUuid() != "GPU-7" || healthGPU.GetUuid() != metricGPU.GetUuid() {
		t.Fatalf("metric GPU=%v health GPU=%v", metricGPU, healthGPU)
	}
	if !proto.Equal(metricGPU.GetNative(), &observation.NativeEntityRef{Source: dcgmsource.Name, EntityKind: 1, EntityId: 7}) {
		t.Fatalf("optional native identities: metric GPU=%v health GPU=%v", metricGPU, healthGPU)
	}
	if !proto.Equal(metricGPU, healthGPU) {
		t.Fatalf("metric and health use different GPU identity shapes: metric=%v health=%v", metricGPU, healthGPU)
	}
}

func TestMeasureHealthPerCallSelectionDoesNotChangeWatches(t *testing.T) {
	source := newFakeHealthSource()
	sensor := configuredHealthSensor(t, source, gpusignals.PowerDraw.ID())
	batch := measureHealthSensor(t, sensor, collect.WithSignalID(dcgmhealth.Report.ID()))
	if len(batch.Observations) != 1 || batch.Observations[0].GetSignalId() != dcgmhealth.Report.ID() {
		t.Fatalf("health-only observations: %v", batch.Observations)
	}
	batch = measureHealthSensor(t, sensor, collect.WithSignalID(gpusignals.PowerDraw.ID()))
	if len(batch.Observations) != 1 || batch.Observations[0].GetSignalId() != gpusignals.PowerDraw.ID() {
		t.Fatalf("metric-only observations: %v", batch.Observations)
	}
	batch = measureHealthSensor(t, sensor, collect.WithSignalID())
	if len(batch.Observations) != 0 {
		t.Fatalf("empty selection observations: %v", batch.Observations)
	}
	if source.metricRegistrations != 1 || source.healthRegistrations != 1 || source.healthWatch != dcgmhealth.KnownSystems {
		t.Fatal("per-call selection changed registered watches")
	}
	cycle, err := collect.NewCycle("invalid-area-selection", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID("dcgm.health.memory"))
	if err == nil {
		t.Fatal("per-area selection unexpectedly succeeded")
	}
	source.assertRequests(t, dcgmsource.CollectionRequest{Health: true}, dcgmsource.CollectionRequest{Metrics: []string{gpusignals.PowerDraw.ID()}})
}

func TestAggregateHealthPreservesAllNativeFindingsAndOwnsClones(t *testing.T) {
	source := newFakeHealthSource()
	raw := source.result.HealthReport
	raw.NativeGroupOverallHealth = observation.DCGMHealthResult(-91).Enum()
	raw.SystemsWithoutTargets = dcgmhealth.NVSwitchSystems
	raw.Entities = append(raw.Entities, rawHealthTarget(77, 0xffffffff))
	raw.Incidents = []*observation.DCGMHealthIncident{
		{System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 100, Message: "first memory fault", Entity: raw.Entities[0]},
		{System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultWarn.Enum(), ErrorCode: 101, Message: "second memory fault", Entity: raw.Entities[0]},
		{System: dcgmhealth.SystemPCIe, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 102, Entity: raw.Entities[0]},
		{System: dcgmhealth.SystemAll, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 103, Entity: rawHealthTarget(0, 0)},
		{System: 0, NativeHealth: observation.DCGMHealthResult(-38).Enum(), ErrorCode: 104, Entity: rawHealthTarget(0, 0)},
		{System: 1 << 40, NativeHealth: observation.DCGMHealthResult(39).Enum(), ErrorCode: 1 << 61, Entity: raw.Entities[2]},
		{System: dcgmhealth.SystemThermal | 1<<41, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 106, Entity: raw.Entities[1]},
	}
	expected := proto.Clone(raw).(*observation.DCGMHealthReport)
	sensor := configuredHealthSensor(t, source)
	first := measureHealthSensor(t, sensor).Observations[0].GetValue().GetDcgmHealthReport()
	second := measureHealthSensor(t, sensor).Observations[0].GetValue().GetDcgmHealthReport()
	if !proto.Equal(first, raw) || !proto.Equal(second, raw) || len(first.Incidents) != 7 {
		t.Fatalf("aggregate report lost native findings or metadata: %v", first)
	}
	first.Entities[0].GetGpu().Uuid = "changed"
	first.Entities[0].GetGpu().Native.EntityId = 99
	first.Entities[1].GetNvSwitch().Native.EntityId = 98
	first.Entities[2].GetNativeEntity().EntityKind = 97
	first.Incidents[0].Message = "changed"
	first.Incidents[0].GetEntity().GetGpu().Native.Source = "changed"
	first.Incidents[3].GetEntity().GetNativeEntity().EntityId = 96
	*first.NativeGroupOverallHealth = observation.DCGMHealthResultPass
	if !proto.Equal(raw, expected) || !proto.Equal(second, expected) {
		t.Fatal("emitted reports alias the source report or one another")
	}
}

func TestAggregateHealthAvailabilityOutcomes(t *testing.T) {
	for _, test := range []struct {
		name        string
		missing     uint64
		requested   uint64
		incident    bool
		wantNoValue observation.NoValueReason
	}{
		{name: "all targets absent", requested: dcgmhealth.KnownSystems, missing: dcgmhealth.KnownSystems, wantNoValue: observation.NoValueReasonNoTargets},
		{name: "partial targets", requested: dcgmhealth.KnownSystems, missing: dcgmhealth.NVSwitchSystems},
		{name: "first report has no incidents", requested: dcgmhealth.KnownSystems},
		{name: "zero requested mask is not all targets absent"},
		{name: "incident is preserved", requested: dcgmhealth.KnownSystems, incident: true},
		{name: "incident survives no targets", requested: dcgmhealth.KnownSystems, missing: dcgmhealth.KnownSystems, incident: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newFakeHealthSource()
			source.result.HealthReport.RequestedSystems = test.requested
			source.result.HealthReport.SystemsWithoutTargets = test.missing
			source.result.HealthReport.MonitoredSince = timestamppb.New(time.Unix(200, 0))
			if test.incident {
				source.result.HealthReport.Incidents = []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(), Entity: source.result.HealthReport.Entities[0]}}
			}
			batch := measureHealthSensor(t, configuredHealthSensor(t, source))
			if len(batch.Observations) != 1 {
				t.Fatalf("health collection observations: %v", batch.Observations)
			}
			current := batch.Observations[0]
			if test.wantNoValue != observation.NoValueReasonUnspecified {
				if current.GetValue() != nil || current.GetNoValue().GetReason() != test.wantNoValue || current.GetTarget() != nil {
					t.Fatalf("availability observation = %v", current)
				}
			} else if report := current.GetValue().GetDcgmHealthReport(); !proto.Equal(report, source.result.HealthReport) {
				t.Fatalf("usable aggregate facts changed or synthetic incidents appeared: %v", current)
			} else if _, ok := current.GetTarget().GetKind().(*observation.ObservationTarget_Node); !ok {
				t.Fatalf("usable health report lost its node target: %v", current)
			}
		})
	}
}

func TestHealthAPIErrorPreservesMetricObservations(t *testing.T) {
	source := newFakeHealthSource()
	source.result.HealthError = &observation.CollectionError{
		Operation: "health setup", Category: observation.CollectionErrorCategoryUnavailable, Detail: "native API failed", SourceError: "-3",
	}
	batch := measureHealthSensor(t, configuredHealthSensor(t, source, gpusignals.PowerDraw.ID()))
	if len(batch.Observations) != 2 || batch.Observations[0].GetValue().GetDoubleValue() != 100 || batch.Observations[0].GetTarget().GetGpu().GetUuid() != "GPU-7" {
		t.Fatalf("partial metric batch = %v", batch)
	}
	expected := proto.Clone(source.result.HealthError).(*observation.CollectionError)
	expected.Source = dcgmsource.Name
	current := batch.Observations[1]
	if current.GetValue() != nil || !proto.Equal(current.GetCollectionError(), expected) {
		t.Fatalf("health API failure became hardware findings: %v", current)
	}
	current.GetCollectionError().Detail = "changed"
	if source.result.HealthError.GetDetail() != "native API failed" {
		t.Fatal("health failure observation shares a mutable source error")
	}
}

func TestMeasurePreservesHealthAlongsideGPUDiscoveryFailure(t *testing.T) {
	for _, metrics := range [][]string{nil, {gpusignals.PowerDraw.ID()}} {
		source := newFakeHealthSource()
		failure := &observation.CollectionError{
			Source: dcgmsource.Name, Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable, Detail: "GPU enumeration failed",
		}
		source.result.DiscoveryError = failure
		source.result.GPUs = nil
		source.result.Inventory.Outcome = &observation.GPUInventorySnapshot_CollectionError{CollectionError: failure}
		batch := measureHealthSensor(t, configuredHealthSensor(t, source, metrics...), collect.WithGPUInventory())
		if len(batch.Observations) != len(metrics)+1 || batch.Observations[len(metrics)].GetValue().GetDcgmHealthReport() == nil ||
			batch.GetInventory().GetGpuSnapshots()[0].GetCollectionError() == nil {
			t.Fatalf("GPU discovery failure suppressed health: %v", batch)
		}
		if len(metrics) > 0 && batch.Observations[0].GetCollectionError() == nil {
			t.Fatalf("missing metric discovery error: %v", batch)
		}
		source.assertRequests(t, dcgmsource.CollectionRequest{Metrics: metrics, Health: true, Inventory: true})
	}
}

func TestHealthSourceFailureAndEmptyReport(t *testing.T) {
	source := newFakeHealthSource()
	source.result.HealthReport = nil
	source.result.HealthError = &observation.CollectionError{
		Category: observation.CollectionErrorCategorySourceError,
		Detail:   "DCGM health check returned no report",
	}
	batch := measureHealthSensor(t, configuredHealthSensor(t, source))
	if len(batch.Observations) != 1 || batch.Observations[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategorySourceError {
		t.Fatalf("missing report = %v", batch)
	}
	source.result.HealthReport = &observation.DCGMHealthReport{}
	source.result.HealthError = nil
	batch = measureHealthSensor(t, configuredHealthSensor(t, source))
	if len(batch.Observations) != 1 || batch.Observations[0].GetValue().GetDcgmHealthReport() == nil || batch.Observations[0].GetCollectionError() != nil {
		t.Fatalf("empty report must remain a value: %v", batch)
	}
}
