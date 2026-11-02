// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

// TestXid154SensorToCatalogSmoke uses only exported APIs and no GPU hardware.
func TestXid154SensorToCatalogSmoke(t *testing.T) {
	transition := func(device, code, name string) string {
		return fmt.Sprintf("NVRM: Xid (PCI:%s): 154, GPU recovery action changed from 0x0 (None) to %s (%s)", device, code, name)
	}
	reboot := transition("0000:01:00", "0x2", "Node Reboot Required")
	none := transition("0000:01:00", "0x0", "None")
	for _, tc := range []struct {
		name     string
		lines    []string
		action   health.RecommendedAction
		severity health.Severity
	}{
		{"node reboot", []string{reboot}, health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"PF FLR", []string{transition("0000:01:00", "0x1", "PF FLR")}, health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired), health.Critical},
		{"drain and reset", []string{transition("0000:01:00", "0x4", "Drain and Reset")}, health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired), health.Critical},
		{"P2P name contains reboot", []string{transition("0000:01:00", "0x3", "reboot is not the code")}, health.Drain(health.RemediationActionXid154), health.Warning},
		{"unknown numeric", []string{transition("0000:01:00", "0xff", "Node Reboot Required")}, health.Monitor, health.Warning},
		{"malformed", []string{transition("0000:01:00", "garbled", "Node Reboot Required")}, health.Monitor, health.Warning},
		{"bare", []string{"NVRM: Xid (PCI:0000:01:00): 154"}, health.Monitor, health.Warning},
		{"bare after reboot keeps reboot", []string{reboot, "NVRM: Xid (PCI:0000:01:00): 154"}, health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"bus reset", []string{transition("0000:01:00", "0x6", "Bus Reset")}, health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"system reboot", []string{transition("0000:01:00", "0x7", "System Reboot")}, health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"None", []string{reboot, none}, health.NoAction, health.Info},
		{"other GPU survives None", []string{reboot, transition("0000:02:00", "0x2", "Node Reboot Required"), none}, health.Drain(health.RemediationActionRestartOS), health.Critical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(_ context.Context, _ []string, stdout, _ io.Writer) error {
				for i, line := range tc.lines {
					// Deliberately equal timestamps: only journal order gives chronology.
					if _, err := fmt.Fprintf(stdout, `{"__CURSOR":"entry-%d","__REALTIME_TIMESTAMP":"1700000000000000","_BOOT_ID":"0123456789abcdef0123456789abcdef","MESSAGE":%q}`+"\n", i, line); err != nil {
						return err
					}
				}
				return nil
			}
			cycle, err := collect.NewCycle("xid154-smoke", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			batch, err := xidsensor.New(journal.New(runner)).Measure(context.Background(), collect.WithCycle(cycle))
			if err != nil {
				t.Fatal(err)
			}
			slices.Reverse(batch.Observations)
			measurements := observations.ToMeasurements(batch.Observations)
			if len(measurements) != 1 {
				t.Fatalf("measurements = %v", measurements)
			}
			m := measurements[0].(xid.XidMeasurement)
			if len(m.Errors()) != 0 {
				t.Fatalf("bridge errors: %v", m.Errors())
			}
			exported, err := observations.FromMeasurements("xid", "", time.Now(), m)
			if err != nil {
				t.Fatal(err)
			}
			restored := observations.ToMeasurements(exported)
			if len(restored) != 1 {
				t.Fatalf("restored measurements = %v", restored)
			}
			if tc.name == "node reboot" {
				r := restored[0].(xid.XidMeasurement).Events[0].RecoveryAction
				if r == nil || r.FromCode != "0x0" || r.FromName != "None" || r.ToCode != "0x2" || r.ToName != "Node Reboot Required" {
					t.Fatalf("persisted transition lost: %+v", r)
				}
			}
			cat := catalog.NewEmpty(catalog.WithArchitecture("H100"), catalog.WithExecutionContext(health.OperatorInfra))
			for _, check := range xid.Checks() {
				cat.MustRegister(check)
			}
			v, err := cat.Evaluate("gpu_xid_154", restored[0])
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != tc.severity || !v.RecommendedAction.Equal(tc.action) {
				t.Fatalf("verdict = %+v", v)
			}
			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "node reboot" {
				overrides := catalog.NewOverrideSet(catalog.Override{CheckID: "gpu_xid_154", ThresholdName: "occurrences", Value: int64(2)})
				thresholdCatalog := catalog.NewEmpty(catalog.WithArchitecture("H100"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(overrides))
				for _, check := range xid.Checks() {
					thresholdCatalog.MustRegister(check)
				}
				below, err := thresholdCatalog.Evaluate("gpu_xid_154", restored[0])
				if err != nil {
					t.Fatal(err)
				}
				if below.Severity != health.Pass {
					t.Fatalf("one report should be below overridden threshold: %+v", below)
				}
			}
			t.Logf("sensor -> observations -> catalog: %s", encoded)
		})
	}
}
