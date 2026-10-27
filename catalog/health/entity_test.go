// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

type hostWide struct{}

func (hostWide) Category() health.Category           { return health.Node }
func (hostWide) Errors() []health.ErroredMeasurement { return nil }

type identified struct{ id string }

func (identified) Category() health.Category           { return health.GPU }
func (identified) Errors() []health.ErroredMeasurement { return nil }
func (m identified) EntityID() string                  { return m.id }

type keyed struct {
	identified
	key health.EntityKey
}

func (m keyed) EntityKey() health.EntityKey { return m.key }

// Measurements predating EntityKeyed keep a key derived from what they
// already expose.
func TestKeyOf(t *testing.T) {
	for _, tc := range []struct {
		m    health.Measurement
		name string
		want health.EntityKey
	}{
		{name: "host-wide", m: hostWide{}, want: health.NodeEntity()},
		{name: "entity ID", m: identified{id: "GPU-A"}, want: health.VerifiedEntity("GPU-A")},
		{name: "empty entity ID", m: identified{}, want: health.EntityKey{}},
		{name: "declared key wins", m: keyed{identified{"label"}, health.ProvisionalEntity("nvml", "3")}, want: health.ProvisionalEntity("nvml", "3")},
	} {
		if got := health.KeyOf(tc.m); got != tc.want {
			t.Errorf("%s: KeyOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEntityKeyValidate(t *testing.T) {
	for _, tc := range []struct {
		key   health.EntityKey
		valid bool
	}{
		{health.EntityKey{}, true},
		{health.NodeEntity(), true},
		{health.VerifiedEntity("GPU-A"), true},
		{health.VerifiedEntity(""), false},
		{health.ProvisionalEntity("nvml", "0"), true},
		{health.ProvisionalEntity("", "0"), false},
		{health.ProvisionalEntity("nvml", ""), false},
		{health.SourceEntity("nvml"), true},
		{health.SourceEntity(""), false},
	} {
		if err := tc.key.Validate(); (err == nil) != tc.valid {
			t.Errorf("%v: Validate = %v, want valid=%v", tc.key, err, tc.valid)
		}
	}
}

// Equal slots from different sources, or a slot and a UUID with the same
// text, are different keys.
func TestEntityKeyIdentity(t *testing.T) {
	distinct := []health.EntityKey{
		health.ProvisionalEntity("nvml", "0"),
		health.ProvisionalEntity("dcgm", "0"),
		health.VerifiedEntity("0"),
		health.SourceEntity("nvml"),
		health.NodeEntity(),
		{},
	}
	for i, a := range distinct {
		for j, b := range distinct {
			if (a == b) != (i == j) {
				t.Errorf("%v == %v is %v", a, b, a == b)
			}
		}
	}
	if !health.ProvisionalEntity("nvml", "0").Provisional() || !health.SourceEntity("nvml").Provisional() || health.VerifiedEntity("x").Provisional() {
		t.Error("Provisional misclassifies a key")
	}
	if !health.SourceEntity("nvml").SourceLevel() || health.ProvisionalEntity("nvml", "0").SourceLevel() {
		t.Error("SourceLevel misclassifies a key")
	}
	slot, source := health.ProvisionalEntity("nvml", "0"), health.SourceEntity("smi")
	if slot.Source() != "nvml" || slot.Slot() != "0" || source.Source() != "smi" || source.Slot() != "" || health.VerifiedEntity("x").Slot() != "" {
		t.Error("Source or Slot misreports a key's parts")
	}
}
