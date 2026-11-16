// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package scope lets a measurement whose signals cover more than one check,
// such as Xid signals for every code on the node, narrow each verdict's
// observed values to the part its check judges. It is internal so the hook
// can change without breaking code outside the module.
package scope

// Scoper narrows a verdict's observed values to the part one check judges.
// It runs after the rules and must not modify Observed, which the rules
// share. It returns Observed for a check it does not know, and must return a
// non-nil map: evaluation reads a non-nil ObservedValues as "a rule matched"
// when attributing dependencies.
type Scoper interface {
	ScopeObservedValues(Request) map[string]any
}

// Request is in an internal package so code outside the module cannot call
// or implement ScopeObservedValues.
type Request struct {
	Observed map[string]any
	CheckID  string
}
