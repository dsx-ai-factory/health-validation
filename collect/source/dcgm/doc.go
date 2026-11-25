// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm collects GPU metrics, passive health reports, and inventory. A
// [Source] connects to an existing hostengine in standalone mode or starts an
// engine in the process in embedded mode. Each process uses one mode.
//
// Reuse one Source across collection cycles. Register the metrics and health
// systems before the first metric or health read. Inventory leaves registration
// open. The client and its watches open lazily.
//
// [Source.Collect] selects metrics, health, and inventory independently and
// returns completed results alongside errors. Concurrent collections may
// interleave native operations. Each collection uses one client session for
// GPU identities and findings. An unbound collection in a different cycle can
// recover a failed session. Pass IDs and identities from the current session
// when calling read methods separately.
//
// [Source.Close] stops new native work, attempts watch and group cleanup while
// the session is valid, and releases the client reference. Health sampling may
// persist in an external hostengine. If Close returns [ErrInUse], retry when
// the native call finishes. Other cleanup errors are final.
//
// See the [collection API guide] for collection order, health sampling, and
// report interpretation.
//
// [collection API guide]: https://github.com/dsx-ai-factory/health-validation/blob/main/docs/collection-api.md#dcgm-metrics-health-and-inventory
package dcgm
