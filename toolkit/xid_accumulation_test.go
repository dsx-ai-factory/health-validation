// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// The whole path from raw journal text: sensors collect measurements, the
// catalog's Xid accumulator derives from them, and the stock per-subcode
// check reads what it derived. The node measurement carrying the driver
// version is collected beside the Xid measurement, whether or not it holds
// a readable version.
func TestCollect_XidSubtypeDerivedByAccumulator(t *testing.T) {
	fault := "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 0x00000000)"

	for name, version := range map[string]string{"known version": "575.51.03", "unreadable version": ""} {
		t.Run(name, func(t *testing.T) {
			cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
			nodeSensor := toolkit.NewNodeSensor(toolkit.MockNode(node.NodeMeasurement{GPUCount: 4, DriverVersion: version}))
			nv, err := toolkit.Collect(cat, toolkit.NewXidSensor(toolkit.MockXid(fault)), nodeSensor)
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			for _, v := range nv.CheckResults {
				if v.CheckID == "gpu_xid_149_4" {
					if v.Severity != health.Critical {
						t.Errorf("gpu_xid_149_4 severity = %v, want CRITICAL", v.Severity)
					}
					return
				}
			}
			t.Fatal("gpu_xid_149_4 produced no verdict")
		})
	}
}
