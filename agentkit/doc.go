// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package agentkit is agent plumbing built on the collect package: the
// server surface (gRPC and HTTP on one port). It is opt-in — collect
// stays usable with no agent involved.
//
// This module is experimental (v0). Its APIs follow the
// shared-collection-library proposal and may change until per-module
// versioning lands.
package agentkit
