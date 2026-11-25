// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm converts DCGM GPU metrics and optional passive health reports
// into observations, with optional GPU inventory attached to the batch.
//
// [New] collects metrics and selects all supported metrics when no IDs are
// supplied. [NewWithHealth] collects only explicitly supplied metrics and
// enables health when its systems mask is nonzero. With no metric IDs, a
// nonzero mask selects health only. Either constructor supports optional GPU
// inventory collection.
//
// Reuse the Source and finish sensor construction before the first metric or
// health read. Pass a collection cycle to each Measure call. Per-call selection
// filters outputs without reconfiguring watches. Each measurement uses one
// client session and preserves completed results if the session fails. Close
// the Source when finished.
//
// Health uses one node-wide dcgm.health observation containing native incident
// entities. An empty incident list does not establish complete coverage or
// healthy status. Consumers supply verdict and recovery policy.
//
// See the [collection API guide] for output selection, GPU identities, and
// interpretation of health reports.
//
// [collection API guide]: https://github.com/dsx-ai-factory/health-validation/blob/main/docs/collection-api.md#dcgm-metrics-health-and-inventory
package dcgm
