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

package catalog

import "testing"

func TestOverrideSet_ForCheck(t *testing.T) {
	os := NewOverrideSet(
		Override{CheckID: "a", ThresholdName: "x", Value: 1},
		Override{CheckID: "a", ThresholdName: "y", Value: 2},
		Override{CheckID: "b", ThresholdName: "z", Value: 3},
	)

	got := os.ForCheck("a")
	if len(got) != 2 {
		t.Errorf("got %d overrides for 'a', want 2", len(got))
	}
	if got["x"] != 1 || got["y"] != 2 {
		t.Errorf("unexpected values: %v", got)
	}
}

func TestOverrideSet_ForCheckMissing(t *testing.T) {
	os := NewOverrideSet()
	if got := os.ForCheck("nope"); got != nil {
		t.Errorf("expected nil for missing check, got %v", got)
	}
}
