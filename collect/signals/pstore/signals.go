// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pstore defines raw persistent kernel record signals, not panic verdicts.
package pstore

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Path          = collect.NewSignal("pstore.record.path", "")
	SHA256        = collect.NewSignal("pstore.record.sha256", "")
	RawMessage    = collect.NewSignal("pstore.record.raw_message", "")
	ContentBase64 = collect.NewSignal("pstore.record.content.base64", "")
	Root          = collect.NewSignal("pstore.record.root", "")
)

// ContentBase64 preserves binary records without lossy UTF-8 conversion.
func All() collect.Signals { return collect.Signals{Path, SHA256, RawMessage, ContentBase64, Root} }
