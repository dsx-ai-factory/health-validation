//go:build !linux

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"errors"
	"os"
	"time"
)

func openKmsg() (*os.File, time.Time, string, error) {
	return nil, time.Time{}, "", errors.ErrUnsupported
}
