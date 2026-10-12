// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeUsageAndHostRootConfinement(t *testing.T) {
	root := t.TempDir()
	usage, err := nativeUsage(root, "/")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total == 0 || usage.Free > usage.Total || usage.Used > usage.Total {
		t.Fatalf("invalid native capacity: %+v", usage)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeUsage(root, "/escape"); err == nil {
		t.Fatal("statfs escaped the selected host root")
	}
}
