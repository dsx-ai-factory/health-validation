// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

// Signal describes one signal a sensor can produce. Domain packages own
// canonical definitions; sensors expose the subset they support. Once an ID is
// published, its meaning and unit do not change; a new meaning gets a new ID.
// External sensors can implement Signal for additional definitions without
// modifying this library.
type Signal interface {
	// ID returns the stable wire identifier.
	ID() string
	// Unit returns the canonical unit, or nil when the signal has no
	// canonical unit.
	Unit() *string
}

// signal is the library's Signal implementation. It stays unexported so every
// definition is constructed through NewSignal.
type signal struct {
	id   string
	unit string
}

// NewSignal returns a signal definition. An empty unit means the signal has no
// canonical unit.
func NewSignal(id, unit string) Signal {
	return signal{id: id, unit: unit}
}

func (definition signal) ID() string {
	return definition.id
}

func (definition signal) Unit() *string {
	if definition.unit == "" {
		return nil
	}
	unit := definition.unit
	return &unit
}

// Signals is a list of signal definitions.
type Signals []Signal

// IDs returns the IDs in their original order.
func (definitions Signals) IDs() []string {
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID())
	}
	return ids
}
