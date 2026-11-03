// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package scope lets a measurement narrow a verdict's observed values to
// the part one check judges. It is internal so the hook is not public API.
package scope

// Scoper is an optional interface a measurement may implement when its
// signals cover more than one check judges, such as Xid signals for every
// code on the node, so each verdict reports only its check's part.
//
// It runs after the rules and must not modify Request.Observed, which the
// rules share. It returns Observed for a check it does not know, and never
// nil: evaluation reads a non-nil ObservedValues as a matched rule when
// attributing dependencies.
type Scoper interface {
	ScopeObservedValues(Request) map[string]any
}

// Request is a struct from this package so that code outside the module
// cannot name it, and so cannot call or implement [Scoper].
type Request struct {
	Observed map[string]any
	CheckID  string
}
