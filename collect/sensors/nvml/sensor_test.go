package nvml

import (
	"context"
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type fakeInstance struct {
	devices    map[string]device.Device
	initErr    error
	refreshErr error
	exists     bool
	refreshed  bool
}

func (f *fakeInstance) NVMLExists() bool                  { return f.exists }
func (f *fakeInstance) Devices() map[string]device.Device { return f.devices }
func (f *fakeInstance) Shutdown() error                   { return nil }
func (f *fakeInstance) InitError() error                  { return f.initErr }

func (f *fakeInstance) Refresh() error {
	f.refreshed = true
	if f.refreshErr != nil {
		f.devices = nil
		f.initErr = f.refreshErr
		return f.refreshErr
	}
	return nil
}

func TestMeasureRefresh(t *testing.T) {
	enumErr := errors.New("error getting device handle for index '4': Unknown Error")

	tests := []struct {
		inst          *fakeInstance
		name          string
		wantOperation string
		opts          []collect.OpOption
		wantCategory  observation.CollectionErrorCategory
		wantRefreshed bool
	}{
		{
			name: "default measures the instantiation-time device list",
			inst: &fakeInstance{exists: true, devices: map[string]device.Device{}},
		},
		{
			name:          "default reports a failed instantiation-time enumeration",
			inst:          &fakeInstance{exists: true, initErr: enumErr},
			wantOperation: "GetDevices",
			wantCategory:  observation.CollectionErrorCategoryUnavailable,
		},
		{
			name:          "with refresh re-enumerates",
			inst:          &fakeInstance{exists: true, devices: map[string]device.Device{}},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantRefreshed: true,
		},
		{
			name:          "failed refresh marks every signal unavailable",
			inst:          &fakeInstance{exists: true, refreshErr: enumErr},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantRefreshed: true,
			wantOperation: "GetDevices",
			wantCategory:  observation.CollectionErrorCategoryUnavailable,
		},
		{
			name:          "nvml absent reports unsupported without refreshing",
			inst:          &fakeInstance{exists: false},
			opts:          []collect.OpOption{collect.WithRefresh(true)},
			wantOperation: "Init",
			wantCategory:  observation.CollectionErrorCategoryUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sensor := New(test.inst)
			batch, err := sensor.Measure(context.Background(), test.opts...)
			if err != nil {
				t.Fatalf("Measure() error = %v", err)
			}
			if test.inst.refreshed != test.wantRefreshed {
				t.Errorf("Refresh() called = %t, want %t", test.inst.refreshed, test.wantRefreshed)
			}

			if test.wantOperation == "" {
				if len(batch.Observations) != 0 {
					t.Fatalf("Measure() emitted %d observations, want 0", len(batch.Observations))
				}
				return
			}
			if len(batch.Observations) != len(temperatureSignals()) {
				t.Fatalf("Measure() emitted %d observations, want one per signal (%d)",
					len(batch.Observations), len(temperatureSignals()))
			}
			for _, obs := range batch.Observations {
				failure := obs.GetCollectionError()
				if failure == nil {
					t.Errorf("signal %q: got a value, want a collection error", obs.GetSignalId())
					continue
				}
				if failure.GetCategory() != test.wantCategory {
					t.Errorf("signal %q: category = %v, want %v", obs.GetSignalId(), failure.GetCategory(), test.wantCategory)
				}
				if failure.GetOperation() != test.wantOperation {
					t.Errorf("signal %q: operation = %q, want %q", obs.GetSignalId(), failure.GetOperation(), test.wantOperation)
				}
			}
		})
	}
}

func TestSensorID(t *testing.T) {
	if got := (&sensor{}).ID(); got != "gpu.nvml" {
		t.Fatalf("Sensor.ID() = %q, want %q", got, "gpu.nvml")
	}
}
