// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	boot := time.Unix(1000, 0)
	msg, err := Parse(boot, "6,9223372036854775807,1234567,-,extra;mlx5: link down; detail\n SUBSYSTEM=pci\n")
	require.NoError(t, err)
	require.Equal(t, 6, msg.Priority)
	require.Equal(t, int64(9223372036854775807), msg.Sequence)
	require.True(t, msg.Timestamp.Equal(boot.Add(1234567*time.Microsecond)))
	require.Equal(t, "mlx5: link down; detail\n SUBSYSTEM=pci\n", msg.Body)
	for _, raw := range []string{"missing", "6,9223372036854775808,1;body", "1,2;body", "1,2,18446744073709551615;body", "1,2,-9223372036854775808;body", "1,2,9223372036854775807;body", "x,2,3;body"} {
		_, parseErr := Parse(boot, raw)
		assert.Error(t, parseErr)
	}
}

func TestParseErrorField(t *testing.T) {
	for _, tc := range []struct{ record, detail string }{
		{"6,bad-sequence,1;body", `"bad-sequence" as sequence number`},
		{"6,1,bad-time;body", `"bad-time" as timestamp`},
	} {
		_, err := Parse(time.Unix(1000, 0), tc.record)
		assert.ErrorContains(t, err, tc.detail)
	}
}
