// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
)

// Every architecture a built-in check is scoped to must be one detection can
// return. A name detection never returns (for example a renamed one) would
// make the check skip that GPU without any error.
func TestArchitecture_CatalogNamesAreDetectable(t *testing.T) {
	defs := catalog.New().List(catalog.Filter{})
	if len(defs) == 0 {
		t.Fatal("built-in catalog has no checks")
	}
	for _, def := range defs {
		names := append([]string(nil), def.Architectures...)
		for _, spec := range def.Thresholds {
			for arch := range spec.PerArchitecture {
				names = append(names, arch)
			}
		}
		for _, arch := range names {
			if got := ArchitectureFromName("NVIDIA " + arch); got != arch {
				t.Errorf("check %s lists architecture %q, but detection maps %q to %q", def.ID, arch, "NVIDIA "+arch, got)
			}
		}
	}
}
