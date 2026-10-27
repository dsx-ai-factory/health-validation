// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pciaddr

import "testing"

func TestParse(t *testing.T) {
	for input, want := range map[string]string{
		"0000:89:00":  "0000:89:00",
		"0008:01:00":  "0008:01:00",
		"10000:01:00": "10000:01:00", // the kernel prints domains above 0xffff in full
	} {
		addr, ok := Parse(input)
		if !ok || addr.String() != want {
			t.Errorf("Parse(%q) = %q, %t; want %q, true", input, addr.String(), ok, want)
		}
	}
	for _, input := range []string{
		"",
		"0000:89",
		"zz00:89:00",
		"0000:89:00.0",
		"0000:8:00",
		"0000:89:000",
		"0000:89:20",
		"000000000:01:00",
		"0000:89:00:00",
	} {
		if addr, ok := Parse(input); ok {
			t.Errorf("Parse(%q) = %q, want rejected", input, addr.String())
		}
	}
}

func TestStringMatchesKernelFormat(t *testing.T) {
	if got := (Address{Domain: 8, Bus: 1, Device: 0}).String(); got != "0008:01:00" {
		t.Fatalf("String() = %q, want 0008:01:00", got)
	}
}
