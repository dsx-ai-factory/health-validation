// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

func TestHealthWatchOptionsDefaultsAndValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		options   *HealthWatchOptions
		wantError string
		want      HealthWatchOptions
	}{
		{name: "nil", want: HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: 10 * time.Minute}},
		{name: "zero", options: &HealthWatchOptions{}, want: HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: 10 * time.Minute}},
		{name: "interval only", options: &HealthWatchOptions{UpdateInterval: time.Minute}, want: HealthWatchOptions{UpdateInterval: time.Minute, MaxKeepAge: 10 * time.Minute}},
		{name: "retention only", options: &HealthWatchOptions{MaxKeepAge: time.Hour}, want: HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: time.Hour}},
		{name: "fractional retention", options: &HealthWatchOptions{MaxKeepAge: 1500 * time.Millisecond}, want: HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: 1500 * time.Millisecond}},
		{name: "negative interval", options: &HealthWatchOptions{UpdateInterval: -time.Second}, wantError: "health update interval"},
		{name: "fractional interval", options: &HealthWatchOptions{UpdateInterval: 1500 * time.Nanosecond}, wantError: "whole microseconds"},
		{name: "negative retention", options: &HealthWatchOptions{MaxKeepAge: -time.Second}, wantError: "health retention"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := freezeHealthWatchOptions(test.options)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("options = %+v, %v; want %q error", got, err, test.wantError)
				}
				return
			}
			if err != nil || *got != test.want {
				t.Fatalf("options = %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
}

func TestInvalidHealthWatchOptionsDoNotDisableMetricsOrInventory(t *testing.T) {
	for _, constructor := range []struct {
		newSource func(*HealthWatchOptions) *Source
		name      string
	}{
		{name: "standalone", newSource: func(options *HealthWatchOptions) *Source {
			return NewStandaloneSource(StandaloneOptions{HealthWatch: options})
		}},
		{name: "embedded", newSource: func(options *HealthWatchOptions) *Source {
			return NewEmbeddedSource(EmbeddedOptions{HealthWatch: options})
		}},
	} {
		t.Run(constructor.name, func(t *testing.T) {
			source := constructor.newSource(&HealthWatchOptions{UpdateInterval: -time.Second})
			native := &fakeNative{}
			source.native, source.nativeCallMu, source.processMode = native, &sync.Mutex{}, nil
			t.Cleanup(func() {
				if err := source.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := source.AddHealthToWatch(0); err != nil {
				t.Fatalf("disabled health rejected: %v", err)
			}
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err == nil {
				t.Fatal("invalid health options accepted")
			}
			if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
				t.Fatal(err)
			}
			cycle := newTestCycle(t, "invalid-health-options")
			identities, failure := source.GPUIdentities(context.Background(), cycle)
			if failure != nil || len(identities) != 1 {
				t.Fatalf("identity read = %v, %v", identities, failure)
			}
			snapshot := source.GPUInventory(context.Background(), cycle, identities)
			if snapshot.GetCollectionError() != nil || len(snapshot.GetInventory().GetEntries()) != 1 {
				t.Fatalf("inventory read = %v", snapshot)
			}
			if value := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})[signalPowerDraw]; value.CollectionError != nil || value.Value == nil {
				t.Fatalf("metric read = %v", value)
			}
		})
	}
}
