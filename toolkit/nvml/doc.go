// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package nvml provides a thin client for the NVIDIA Management Library (NVML).
// It wraps go-nvml (github.com/NVIDIA/go-nvml) and manages the NVML session
// lifecycle. Device handles are fetched lazily on first use and cached; a GPU
// whose handle cannot be retrieved is substituted with an errored placeholder
// so a single broken device does not prevent initialization or enumeration.
package nvml
