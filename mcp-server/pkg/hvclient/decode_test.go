// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeVerdict(t *testing.T) {
	good := goodVerdict(t)

	verdict, err := DecodeVerdict(good)
	require.NoError(t, err)
	assert.Equal(t, SchemaVersion, verdict.GetSchemaVersion())

	var fields map[string]any
	require.NoError(t, json.Unmarshal(good, &fields))

	with := func(change func(map[string]any)) []byte {
		copied := map[string]any{}
		for key, value := range fields {
			copied[key] = value
		}

		change(copied)

		raw, err := json.Marshal(copied)
		require.NoError(t, err)

		return raw
	}

	// result changes a copy of the first check result.
	result := func(change func(map[string]any)) func(map[string]any) {
		return func(m map[string]any) {
			first := map[string]any{}
			for key, value := range m["check_results"].([]any)[0].(map[string]any) {
				first[key] = value
			}

			change(first)
			m["check_results"] = []any{first}
		}
	}

	cases := map[string]struct {
		body []byte
		kind Kind
	}{
		"not JSON":             {[]byte("<html>"), KindBadVerdict},
		"an array":             {[]byte("[]"), KindBadVerdict},
		"no fields":            {[]byte("{}"), KindUnsupportedSchema},
		"schema 0.6":           {with(func(m map[string]any) { m["schema_version"] = "0.6" }), KindUnsupportedSchema},
		"no severity":          {with(func(m map[string]any) { delete(m, "overall_severity") }), KindBadVerdict},
		"unspecified severity": {with(func(m map[string]any) { m["overall_severity"] = "SEVERITY_UNSPECIFIED" }), KindBadVerdict},
		"unknown name":         {with(func(m map[string]any) { m["overall_severity"] = "GREAT" }), KindBadVerdict},
		"unknown number":       {with(func(m map[string]any) { m["overall_severity"] = 17 }), KindBadVerdict},
		"unknown domain": {
			with(func(m map[string]any) { m["severity_by_domain"] = map[string]any{"disk": "PASS"} }), KindBadVerdict,
		},
		"unknown number in a map": {
			with(func(m map[string]any) { m["severity_by_domain"] = map[string]any{"cpu": 9} }), KindBadVerdict,
		},
		"unknown number in a list": {
			with(func(m map[string]any) {
				m["check_results"] = []any{map[string]any{"check_id": "cpu_count", "category": 40}}
			}), KindBadVerdict,
		},
		"unknown number in a nested message": {
			with(func(m map[string]any) { m["recommended_action"] = map[string]any{"operational_state": 8} }),
			KindBadVerdict,
		},
		"a wrong type": {with(func(m map[string]any) { m["check_results"] = "none" }), KindBadVerdict},
		"unknown category name in a check": {
			with(result(func(r map[string]any) { r["category"] = "pcie_gen7" })), KindBadVerdict,
		},
		"unknown severity name in a check": {
			with(result(func(r map[string]any) { r["severity"] = "SUPER_CRITICAL" })), KindBadVerdict,
		},
		"no category in a check": {with(result(func(r map[string]any) { delete(r, "category") })), KindBadVerdict},
		"no severity in a check": {with(result(func(r map[string]any) { delete(r, "severity") })), KindBadVerdict},
		"unknown name in a domain map": {
			with(func(m map[string]any) { m["severity_by_domain"] = map[string]any{"cpu": "FATAL"} }), KindBadVerdict,
		},
		"unknown node state name": {
			with(func(m map[string]any) {
				m["recommended_action"] = map[string]any{"operational_state": "QUARANTINE_FOREVER"}
			}), KindBadVerdict,
		},
		"a node action with no state": {
			with(func(m map[string]any) {
				m["recommended_action"] = map[string]any{"remediation_actions": []any{"reset_gpu"}}
			}), KindBadVerdict,
		},
		"overall severity below a check's": {
			with(result(func(r map[string]any) { r["severity"] = "CRITICAL" })), KindBadVerdict,
		},
		"a duplicate key": {
			[]byte(`{"schema_version":"0.7","overall_severity":"PASS","overall_severity":"CRITICAL"}`), KindBadVerdict,
		},
		"data after the verdict": {append(slices.Clone(good), []byte(` {}`)...), KindBadVerdict},
		"nested too deep": {
			[]byte(`{"x":` + strings.Repeat("[", 100) + strings.Repeat("]", 100) + `}`), KindBadVerdict,
		},
	}

	for name, c := range cases {
		_, err := DecodeVerdict(c.body)
		assert.Equal(t, c.kind, KindOf(err), name)
	}

	added := with(func(m map[string]any) { m["added_in_a_later_serve"] = map[string]any{"x": 1} })
	_, err = DecodeVerdict(added)
	assert.NoError(t, err, "unknown fields are ignored")

	nested := with(result(func(r map[string]any) { r["added_in_a_later_serve"] = []any{"x"} }))
	_, err = DecodeVerdict(nested)
	assert.NoError(t, err, "unknown fields are ignored at any depth")
}
