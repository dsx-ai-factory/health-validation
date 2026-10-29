// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rdmatopo

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Diagnostics follow the rdma-core kernel-boot/rdma_topo check output.
const (
	dmaHeader    = "OK\tAll ConnectX DMA functions have correct PCI topology\n"
	inlineHeader = "OK\tAll NIC/GPU complexes have correct PCI topology\n"
	numaHeader   = "OK\tAll NUMA-based complexes have correct PCI topology\n"
	numaFail     = "FAIL\tMultiple devices share the same kernel iommu_group for NUMA node 0: [7, 7]\n"
	acsOK        = "OK\tACS for cx_dma 0000:01:00.0 has correct values 0000000 = xx000x0\n"
	acsFail      = "FAIL\tACS for cx_dma 0000:01:00.0 has incorrect values 1111111 != xx000x0, (0x7f != 0x62)\n"
	dmaOK        = "OK\tKernel iommu_group for DMA 0000:01:00.0 and GPU 0000:02:00.0 are both 7\n"
	dmaFail      = "FAIL\tKernel iommu_group for DMA 0000:01:00.0 and GPU 0000:02:00.0 are not equal 7 != 8\n"
	atsOK        = "OK\tATS capability for cx_nic 0000:01:00.0 is not available\n"
	inlineOK     = "OK\tKernel iommu_group for cx_nic 0000:01:00.0 and nvgpu 0000:02:00.0 are not set\n"
)

func outputRunner(output string, status int) Runner {
	return func(_ context.Context, out, _ io.Writer) (int, error) {
		_, err := io.WriteString(out, output)
		return status, err
	}
}

func TestRead(t *testing.T) {
	tests := []struct {
		name, output     string
		exit, acs, iommu int
		wantError        bool
	}{
		{"DMA healthy", dmaHeader + acsOK + dmaOK, 0, 0, 0, false},
		{"ACS and IOMMU failures", dmaHeader + acsFail + dmaFail, 100, 1, 1, false},
		{"inline healthy", inlineHeader + acsOK + atsOK + inlineOK, 0, 0, 0, false},
		{"inline virtual failure", inlineHeader + acsOK + strings.Replace(atsOK, "not available", "available", 1) + "FAIL\tKernel iommu_group for cx_nic 0000:01:00.0 and nvgpu 0000:02:00.0 are equal 7 == 7\n", 100, 0, 1, false},
		{"NUMA healthy", numaHeader + acsOK + atsOK + "OK\tAll kernel iommu_groups for NUMA node 0 are unique\n", 0, 0, 0, false},
		{"NUMA shared group", numaHeader + acsOK + atsOK + numaFail, 100, 0, 1, false},
		{"empty", "", 0, 0, 0, true},
		{"unsupported", "E: No supported topology detected\n", 100, 0, 0, true},
		{"permission", "E: Cannot read sysfs. Are you root?\n", 100, 0, 0, true},
		{"unreadable ACS", dmaHeader + "FAIL\tCould not read ACS control register for cx_dma 0000:01:00.0\n" + dmaFail, 100, 0, 1, true},
		{"truncated", dmaHeader + acsFail + "FAIL\tKernel iommu_group for DMA ", 100, 1, 0, true},
		{"unknown diagnostic", dmaHeader + acsFail + dmaOK + "OK\tnew check format\n", 100, 1, 0, true},
		{"out of order finding", dmaHeader + acsOK + dmaOK + acsFail, 100, 1, 0, true},
		{"mismatched topology finding", numaHeader + acsOK + atsOK + dmaFail, 100, 0, 1, true},
		{"trailing ATS", inlineHeader + acsFail + atsOK + inlineOK + atsOK, 100, 1, 0, true},
		{"contradictory finding", dmaHeader + strings.Replace(acsFail, "0x7f", "0x7e", 1) + dmaOK, 100, 0, 0, true},
		{"nonzero exit alone", dmaHeader + acsOK + dmaOK, 100, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(outputRunner(tt.output, tt.exit)).Read(context.Background(), nil)
			if tt.wantError {
				require.Error(t, r.Err)
			} else {
				require.NoError(t, r.Err)
			}
			require.Len(t, r.ACSFailures, tt.acs)
			require.Len(t, r.IOMMUGroupFailures, tt.iommu)
			for _, f := range append(r.ACSFailures, r.IOMMUGroupFailures...) {
				if tt.name == "NUMA shared group" {
					require.Equal(t, Finding{Diagnostic: strings.TrimSuffix(numaFail, "\n"), Actual: "[7, 7]", Expected: "unique IOMMU groups"}, f)
				} else {
					require.NotEmpty(t, f.BDFs)
				}
				require.NotEmpty(t, f.Actual)
				require.NotEmpty(t, f.Expected)
				require.True(t, strings.HasPrefix(f.Diagnostic, "FAIL\t"))
			}
		})
	}
}

func TestReadExecutable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	missing := New(nil).Read(context.Background(), nil)
	require.ErrorIs(t, missing.Err, exec.ErrNotFound)
	script := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = check ] && [ \"$PYTHONUNBUFFERED\" = 1 ] || exit 2\nprintf '%s' '" + dmaHeader + acsFail + "'\nwhile :; do :; done\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rdma_topo"), []byte(script), 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := New(nil).Read(ctx, func(r Result) {
		if len(r.ACSFailures) > 0 {
			cancel()
		}
	})
	require.ErrorIs(t, r.Err, context.Canceled)
	require.Len(t, r.ACSFailures, 1)
}
