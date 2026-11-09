// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm manages the DCGM client, metric watch, native-call safety, and
// conversion of DCGM failures into collection errors. A source either connects
// to a running nv-hostengine (standalone) or starts DCGM inside the process
// (embedded); a process uses one mode.
package dcgm
