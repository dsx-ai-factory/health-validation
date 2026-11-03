// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package node_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

func TestParsedDriverVersion(t *testing.T) {
	got := node.NodeMeasurement{DriverVersion: "575.51.03"}.ParsedDriverVersion()
	if !got.AtLeast(575, 0, 0) || got.Raw != "575.51.03" {
		t.Errorf("ParsedDriverVersion() = %+v, want 575.51.03", got)
	}
}

// A failed or unreadable version must read as unknown, never as some
// particular (old) driver. A failure of another signal leaves it readable.
func TestParsedDriverVersionUnknown(t *testing.T) {
	failed := node.NodeMeasurement{DriverVersion: "575.51.03"}
	failed.AddError(errors.New("SystemGetDriverVersion failed"), node.SignalDriverVersion)
	for name, m := range map[string]node.NodeMeasurement{
		"failed":     failed,
		"whole node": node.ErroredNode(errors.New("NVML unavailable")),
		"unreadable": {DriverVersion: "not-a-version"},
		"absent":     {},
	} {
		if got := m.ParsedDriverVersion(); got.Known() {
			t.Errorf("%s: ParsedDriverVersion() = %+v, want unknown", name, got)
		}
	}
	countFailed := node.NodeMeasurement{DriverVersion: "575.51.03"}
	countFailed.AddError(errors.New("enumeration failed"), node.SignalGPUCount)
	if !countFailed.ParsedDriverVersion().Known() {
		t.Error("a gpu_count failure hid the driver version")
	}
}
