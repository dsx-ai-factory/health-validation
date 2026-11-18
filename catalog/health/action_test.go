// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"encoding/json"
	"testing"
)

func TestGenericDrain_JSON(t *testing.T) {
	got, err := json.Marshal(GenericDrain())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"remediation_actions":["reset_gpu","restart_os","service_required"],"operational_state":"drain"}`
	if string(got) != want {
		t.Errorf("GenericDrain() JSON = %s, want %s", got, want)
	}
}

func TestMonitorWith_JSON(t *testing.T) {
	v := CheckVerdict{
		Severity:          Critical,
		RecommendedAction: MonitorWith(RemediationActionUpdateSWFW),
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	const want = `{"remediation_actions":["update_swfw"],"operational_state":"monitor"}`
	if got := string(fields["recommended_action"]); got != want {
		t.Errorf("recommended_action = %s, want %s", got, want)
	}
}
