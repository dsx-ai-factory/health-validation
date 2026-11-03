// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

// Host is what derived signals may know about the node beyond their own
// measurement. It is supplied once, when the catalog is built, and holds
// for every measurement in that run.
//
// The GPU architecture is deliberately not here: it decides which checks
// apply and which thresholds they use, not what a measurement's signals
// are.
type Host struct {
	// DriverVersion is the node's NVIDIA driver version, for derivations
	// whose meaning changed between driver releases. The zero value means
	// it could not be determined.
	DriverVersion DriverVersion
}
