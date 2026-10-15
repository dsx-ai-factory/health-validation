// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package disk

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeUsageHostRootConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeUsage(root, Mount{Point: "/escape", ID: 1}); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, os.ErrNotExist) {
		t.Fatal("statfs escaped the selected host root")
	}
}
