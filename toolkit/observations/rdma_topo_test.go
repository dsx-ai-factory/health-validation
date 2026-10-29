// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"reflect"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestRDMATopoConversion(t *testing.T) {
	cat := catalog.NewEmpty(catalog.WithArchitecture("test"), catalog.WithExecutionContext(health.OperatorInfra))
	cat.MustRegister(pcie.RDMATopo())
	finding := pcie.Finding{Diagnostic: "configuration mismatch", BDFs: []string{"0000:01:00.0"}, Actual: "1", Expected: "0"}
	for _, name := range []string{"healthy", "ACS", "IOMMU", "malformed sibling"} {
		t.Run(name, func(t *testing.T) {
			m := pcie.RDMATopoMeasurement{
				ACSFailures: []pcie.Finding{}, IOMMUGroupFailures: []pcie.Finding{},
				Evidence: pcie.Evidence{
					Host: "node", Executable: "/usr/bin/rdma_topo", ToolVersion: "unknown",
					Mode: "auto", Invocation: []string{"rdma_topo", "check"},
				},
			}
			severity, count := health.Pass, 1
			if name != "healthy" {
				severity, m.Evidence.ExitCode = health.Critical, 100
				if name == "IOMMU" {
					m.IOMMUGroupFailures = []pcie.Finding{finding}
				} else {
					m.ACSFailures = []pcie.Finding{finding}
				}
			}
			obs, err := FromMeasurements("pcie", "node", time.Unix(1, 0), m)
			if err != nil {
				t.Fatal(err)
			}
			if name == "malformed sibling" {
				count = 2
				obs[0].Outcome = &observation.Observation_Value{Value: stringValue(`[{"diagnostic":42},{"diagnostic":"configuration mismatch","bdfs":["0000:01:00.0"],"actual":"1","expected":"0"}]`)}
			}
			converted := ToMeasurements(obs)
			obs, err = FromMeasurements("pcie", "node", time.Unix(1, 0), converted...)
			if err != nil {
				t.Fatal(err)
			}
			got := ToMeasurements(obs)
			if len(got) != count || !reflect.DeepEqual(got[0], m) {
				t.Fatalf("findings or evidence changed: %+v", got)
			}
			verdict, err := cat.EvaluateAll(got...)
			if err != nil {
				t.Fatal(err)
			}
			if v := verdict.CheckResults[0]; v.Skipped || v.Severity != severity {
				t.Fatalf("unexpected finding verdict: %+v", v)
			}
			if count == 2 {
				v := verdict.CheckResults[1]
				if len(got[1].Errors()) == 0 || v.Severity != health.Critical || v.ObservedValues != nil || !v.RecommendedAction.Equal(health.Drain(health.RemediationActionCheckCollection)) {
					t.Fatalf("malformed record lost its separate collection error: %+v", v)
				}
			}
		})
	}
}
