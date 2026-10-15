// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package health defines the domain types for the NVIDIA Canonical Health
// Checks knowledge layer.
//
// These types represent the shared vocabulary between check authors,
// the catalog evaluation engine, and consumers of health verdicts.
// They carry no evaluation logic and have no dependencies beyond
// the standard library.
//
// # Severity model
//
// Every check produces one of five severity levels:
//
//   - Pass: no condition detected
//   - Info: anomaly within safe operating bounds
//   - Warning: degrades performance or increases risk
//   - Critical: imminent failure risk; node should leave service
//   - Unknown: check could not complete
//
// Checks that cannot run (wrong architecture, missing signals, disabled by
// operator) are reported as skipped with a machine-readable reason code and
// excluded from severity aggregation.
//
// # Threshold governance
//
// Thresholds are classified as Default (operator-adjustable), Fixed (cannot be
// overridden), or Adjustable (operator-tunable within bounds). Operators supply
// overrides via a parameter file; the catalog enforces governance rules.
package health
