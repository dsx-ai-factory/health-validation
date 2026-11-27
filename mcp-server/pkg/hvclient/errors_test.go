// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Each hint reaches the model, so each says what to do next, after a semicolon.
func TestEveryHintNamesANextStep(t *testing.T) {
	for kind, hint := range hints {
		_, next, found := strings.Cut(hint, "; ")
		assert.True(t, found && next != "", "%s: %q", kind, hint)
	}

	assert.Contains(t, hints[KindUnsupportedSchema], "schema "+SchemaVersion)
}
