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

// Package catalog is the registry of NVIDIA Canonical Health Checks and the
// entry point for evaluating measurements against them.
//
// A [Catalog] holds [health.CheckDefinition]s — the operational judgment
// NVIDIA encodes about GPU node health — and exposes [Catalog.EvaluateAll]
// to turn typed measurements into a [health.NodeVerdict] consumable by
// automation. Built-in NVIDIA checks are loaded automatically by [New];
// callers can register additional checks via [Catalog.Register].
//
// # Basic use
//
// Construct a catalog with the host's architecture and execution context,
// then evaluate measurements supplied by a sensor:
//
//	cat := catalog.New(
//	    catalog.WithArchitecture("GB200"), // any architecture identifier the catalog knows about
//	    catalog.WithExecutionContext(health.OperatorInfra),
//	)
//
//	m := gpu.GPUMeasurement{GPUID: "GPU-0"}
//	nv, err := cat.EvaluateAll(m)
//	if err != nil { /* handle */ }
//	os.Exit(nv.ExitCode())
//
// Each measurement is matched to checks by [health.Category]; one
// CheckVerdict is produced per (measurement, matching check) pair, and
// [health.AggregateNodeVerdict] rolls them into the node verdict.
//
// # Architectures
//
// Each [health.CheckDefinition] declares the architectures it applies
// to (e.g. "GB200", "H100", "H200", "A100"). At evaluation time the
// catalog skips checks whose architecture list does not include the
// configured one, so architecture-specific thresholds and rules
// coexist in the same registry. New architectures slot in by adding
// their identifier to an existing check's list or by registering a
// new architecture-specific check.
//
// # Operator overrides
//
// Adjustable thresholds (e.g. expected GPU count per fleet) accept
// operator overrides via [WithOverrides] and an [OverrideSet] built from
// [Override] entries. Fixed thresholds reject overrides at construction
// time with a [health.FixedThresholdError].
//
// # Domain check packages
//
// Per-domain check definitions (node, NVLink, XID, …) live in
// subpackages such as [catalog/node], [catalog/nvlink], and
// [catalog/xid]. The catalog's [New] constructor already registers
// them; consumers seldom need to import the domain packages directly.
package catalog
