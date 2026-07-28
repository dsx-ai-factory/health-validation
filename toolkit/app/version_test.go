/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const versionProg = "go-health-validation"

func TestVersionDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := versionCommand(nil, &out, &errOut, versionProg); code != 0 {
		t.Fatalf("versionCommand() = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "go-health-validation ") {
		t.Errorf("version output = %q, want it to start with %q", out.String(), "go-health-validation ")
	}
}

func TestVersionShort(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := versionCommand([]string{"--short"}, &out, &errOut, versionProg); code != 0 {
		t.Fatalf("versionCommand(--short) = %d, want 0; stderr=%q", code, errOut.String())
	}
	// Unstamped test build reports the "dev" sentinel as a bare line.
	if got := strings.TrimSpace(out.String()); got != "dev" {
		t.Errorf("version --short = %q, want %q", got, "dev")
	}
}

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := versionCommand([]string{"--output", "json"}, &out, &errOut, versionProg); code != 0 {
		t.Fatalf("versionCommand(--output json) = %d, want 0; stderr=%q", code, errOut.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("version --output json: not valid JSON: %v\noutput=%q", err, out.String())
	}
	for _, key := range []string{"version", "commit", "date", "goVersion"} {
		if _, ok := m[key]; !ok {
			t.Errorf("version JSON missing key %q; got %v", key, m)
		}
	}
}

func TestVersionUnknownOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := versionCommand([]string{"--output", "yaml"}, &out, &errOut, versionProg); code != exitError {
		t.Fatalf("versionCommand(--output yaml) = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut.String(), "unknown version output") {
		t.Errorf("stderr = %q, want it to mention the unknown output format", errOut.String())
	}
}

func TestVersionShortOutputConflict(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := versionCommand([]string{"--short", "--output", "json"}, &out, &errOut, versionProg); code != exitError {
		t.Fatalf("versionCommand(--short --output json) = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want it to flag the --short/--output conflict", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written on the conflict path", out.String())
	}
}
