// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package gpu defines the per-GPU measurement type ([GPUMeasurement])
// and the GPU-category check definitions that evaluate it
// ([GPUAveragePowerDraw], [GPUPowerLimit], [GPUPowerState],
// [GPUSensorThrottle], [GPUComputeApps]).
//
// XID-driven GPU checks live in [catalog/xid] and node-wide checks
// (e.g. GPU count) live in [catalog/node]. Import this package to
// construct or inspect GPU measurements that the sensors produce.
package gpu
