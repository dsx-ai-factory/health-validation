// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalogview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gpu_xid_154 has CRITICAL rules for a GPU reset and for a reboot, so the severity alone
// cannot say which matched, and no guidance is better than the wrong one.
func TestRuleMessageNeedsOneMessagePerSeverity(t *testing.T) {
	definition, err := Catalog().Get("gpu_xid_154")
	require.NoError(t, err)

	messages := map[string]bool{}

	for _, rule := range definition.Rules {
		if rule.Severity.String() == "CRITICAL" {
			messages[rule.Message] = true
		}
	}

	require.Greater(t, len(messages), 1, "the premise: gpu_xid_154 has CRITICAL rules that say different things")
	assert.Empty(t, RuleMessage("gpu_xid_154", "CRITICAL"))

	assert.NotEmpty(t, RuleMessage("cpu_count", "CRITICAL"), "one CRITICAL rule, so one message")
	assert.Empty(t, RuleMessage("no_such_check", "CRITICAL"))
}
