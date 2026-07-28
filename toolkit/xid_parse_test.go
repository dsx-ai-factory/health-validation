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

package toolkit_test

import (
	"testing"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestParseXid_FullLine(t *testing.T) {
	m := toolkit.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	if m.Since != "boot" {
		t.Errorf("Since = %q, want boot", m.Since)
	}
	if got := m.XidCounts[79]; got != 1 {
		t.Errorf("XidCounts[79] = %d, want 1", got)
	}

	ev := m.Events[0]
	if ev.XidCode != 79 {
		t.Errorf("XidCode = %d, want 79", ev.XidCode)
	}
	if ev.Pid != 1234 {
		t.Errorf("Pid = %d, want 1234", ev.Pid)
	}
	if ev.ProcessName != "python3" {
		t.Errorf("ProcessName = %q, want python3", ev.ProcessName)
	}
	if ev.PCIAddress != "0000:01:00" {
		t.Errorf("PCIAddress = %q, want 0000:01:00", ev.PCIAddress)
	}
}

func TestParseXid_CodeOnly(t *testing.T) {
	m := toolkit.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 31, Ch 0000000a, intr 10000000",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	ev := m.Events[0]
	if ev.XidCode != 31 {
		t.Errorf("XidCode = %d, want 31", ev.XidCode)
	}
	if ev.Pid != 0 {
		t.Errorf("Pid = %d, want 0", ev.Pid)
	}
	if ev.ProcessName != "" {
		t.Errorf("ProcessName = %q, want empty", ev.ProcessName)
	}
}

func TestParseXid_RepeatedCodeIncrementsCount(t *testing.T) {
	m := toolkit.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1, name=a",
		"NVRM: Xid (PCI:0000:02:00): 79, pid=2, name=b",
		"NVRM: Xid (PCI:0000:03:00): 31, pid=3, name=c",
	})

	if m.EventCount != 3 {
		t.Fatalf("EventCount = %d, want 3", m.EventCount)
	}
	if got := m.XidCounts[79]; got != 2 {
		t.Errorf("XidCounts[79] = %d, want 2", got)
	}
	if got := m.XidCounts[31]; got != 1 {
		t.Errorf("XidCounts[31] = %d, want 1", got)
	}
	if len(m.Events) != 3 {
		t.Errorf("len(Events) = %d, want 3", len(m.Events))
	}
}

func TestParseXid_WithJournalPrefix(t *testing.T) {
	m := toolkit.ParseXid("boot", []string{
		"Jun 09 12:00:00 node1 kernel: NVRM: Xid (PCI:0000:65:00): 48, pid=999, name=cuda-app",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	ev := m.Events[0]
	if ev.XidCode != 48 || ev.ProcessName != "cuda-app" || ev.PCIAddress != "0000:65:00" {
		t.Errorf("parsed %+v, want code=48 name=cuda-app pci=0000:65:00", ev)
	}
}

func TestParseXid_NonMatchingLinesSkipped(t *testing.T) {
	m := toolkit.ParseXid("boot", []string{
		"",
		"NVRM: GPU at 0000:01:00.0: GPU has fallen off the bus.",
		"some unrelated kernel message",
		"NVRM: Xid (PCI:0000:01:00): 13, pid=42, name=worker",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1 (only the Xid line)", m.EventCount)
	}
	if m.Events[0].XidCode != 13 {
		t.Errorf("XidCode = %d, want 13", m.Events[0].XidCode)
	}
}

func TestParseXid_NoLines_EmptyMeasurement(t *testing.T) {
	m := toolkit.ParseXid("boot", nil)

	if m.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", m.EventCount)
	}
	if len(m.Events) != 0 {
		t.Errorf("len(Events) = %d, want 0", len(m.Events))
	}
	if m.Category() != health.Xid {
		t.Errorf("Category = %v, want Xid", m.Category())
	}
}
