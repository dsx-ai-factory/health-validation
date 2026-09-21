// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	boot := time.Unix(1000, 0)
	msg, err := Parse(boot, "6,9223372036854775807,1234567,-,extra;mlx5: link down; detail\n SUBSYSTEM=pci\n")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Priority != 6 || msg.Sequence != 9223372036854775807 || !msg.Timestamp.Equal(boot.Add(1234567*time.Microsecond)) || msg.Body != "mlx5: link down; detail\n SUBSYSTEM=pci\n" {
		t.Fatalf("message: %+v", msg)
	}
	for _, raw := range []string{"missing", "6,9223372036854775808,1;body", "1,2;body", "1,2,18446744073709551615;body", "1,2,-9223372036854775808;body", "1,2,9223372036854775807;body", "x,2,3;body"} {
		if _, parseErr := Parse(boot, raw); parseErr == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestParseErrorField(t *testing.T) {
	for _, tc := range []struct{ record, detail string }{
		{"6,bad-sequence,1;body", `"bad-sequence" as sequence number`},
		{"6,1,bad-time;body", `"bad-time" as timestamp`},
	} {
		_, err := Parse(time.Unix(1000, 0), tc.record)
		if err == nil || !strings.Contains(err.Error(), tc.detail) {
			t.Errorf("Parse(%q) error = %v, want %q", tc.record, err, tc.detail)
		}
	}
}
