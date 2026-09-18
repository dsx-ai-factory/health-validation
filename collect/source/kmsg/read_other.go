//go:build !linux

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"errors"
)

// ReadAll is available only on Linux. Parsing is platform-independent.
func ReadAll(context.Context) ([]Message, error) { return nil, errors.ErrUnsupported }
