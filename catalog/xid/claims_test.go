// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import "testing"

// Every shipped check covers exactly its own code, which is what keeps that
// code out of the catch-alls and its verdict to its own events. The catch-alls
// cover nothing. A check ID that does not name its code is a table drifting
// from Checks.
func TestBuiltinClaimsMatchTheShippedChecks(t *testing.T) {
	claims := builtinClaims()
	for _, cd := range Checks() {
		codes, ok := claims[cd.ID]
		if cd.ID == unknownCheckID || cd.ID == informationalCheckID {
			if ok {
				t.Errorf("%s covers %v, want nothing", cd.ID, codes)
			}
			continue
		}
		if !ok || len(codes) != 1 {
			t.Errorf("%s covers %v, want exactly one code", cd.ID, codes)
			continue
		}
		if code, isCode := parseCode(codes[0]); isCode {
			if cd.ID != codeCheckID(code) {
				t.Errorf("%s covers code %d, whose check is %s", cd.ID, code, codeCheckID(code))
			}
		} else if cd.ID != subXidCodeCheckID(codes[0]) {
			t.Errorf("%s covers subtype %s, whose check is %s", cd.ID, codes[0], subXidCodeCheckID(codes[0]))
		}
	}
	if want := len(Checks()) - 2; len(claims) != want {
		t.Errorf("%d claims for %d checks that cover codes", len(claims), want)
	}
}
