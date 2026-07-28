/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
