// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestDiscoverDriverVersion(t *testing.T) {
	proc, _ := health.ParseDriverVersion("575.51.03")
	procOK := func() (health.DriverVersion, bool) { return proc, true }
	procMissing := func() (health.DriverVersion, bool) { return health.DriverVersion{}, false }
	nvmlOK := func() (string, error) { return "580.65.06", nil }
	nvmlFails := func() (string, error) { return "", errors.New("nvml unavailable") }

	tests := []struct {
		fromProc func() (health.DriverVersion, bool)
		fromNVML func() (string, error)
		name     string
		want     string
		wantOK   bool
	}{
		{name: "proc wins", fromProc: procOK, fromNVML: nvmlOK, want: "575.51.03", wantOK: true},
		{name: "nvml fallback", fromProc: procMissing, fromNVML: nvmlOK, want: "580.65.06", wantOK: true},
		{name: "neither", fromProc: procMissing, fromNVML: nvmlFails},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := discoverDriverVersion(tt.fromProc, tt.fromNVML)
			if ok != tt.wantOK || got.Raw != tt.want {
				t.Errorf("discoverDriverVersion = (%q, %v), want (%q, %v)", got.Raw, ok, tt.want, tt.wantOK)
			}
		})
	}
}
