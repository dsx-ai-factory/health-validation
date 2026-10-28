// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

func linkflapCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

// outages builds a measurement carrying only the headline signal; the
// per-outage detail is exercised by the toolkit parser tests.
func outages(seconds ...int64) infiniband.LinkFlapMeasurement {
	return infiniband.LinkFlapMeasurement{OutageDurations: seconds, Since: "boot"}
}

func thresholdOverride(name string, value any) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "rdma_linkflap_fatal",
		ThresholdName: name,
		Value:         value,
	}))
}

func TestRDMALinkFlapFatalDefinition(t *testing.T) {
	def := infiniband.RDMALinkFlapFatal()
	if def.ID != "rdma_linkflap_fatal" || def.Name != "RDMA Link Flap (Fatal)" {
		t.Errorf("identity = %q/%q", def.ID, def.Name)
	}
	if def.Category != health.InfiniBand {
		t.Errorf("Category = %v, want InfiniBand", def.Category)
	}
	if got, want := def.RequiredSignals, []string{infiniband.SignalOutageDurations}; !slices.Equal(got, want) {
		t.Errorf("RequiredSignals = %v, want %v", got, want)
	}
	if got, want := def.Architectures, []string{"GB200", "GB300", "B200"}; !slices.Equal(got, want) {
		t.Errorf("Architectures = %v, want %v", got, want)
	}
	if !def.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("RecommendedAction = %v, want check_network drain", def.RecommendedAction)
	}
	for _, name := range []string{"min_outage_seconds", "fatal_flap_threshold"} {
		spec, ok := def.Thresholds[name]
		if !ok {
			t.Errorf("missing threshold %q", name)
			continue
		}
		if spec.Category != health.DefaultThreshold {
			t.Errorf("%s Category = %v, want DefaultThreshold", name, spec.Category)
		}
	}
	if len(def.Rules) != 1 || def.Rules[0].Severity != health.Critical {
		t.Fatalf("Rules = %v, want one CRITICAL rule", def.Rules)
	}
}

// No outages, and outages shorter than the 30s window, are not fatal flaps.
func TestRDMALinkFlapFatal_Pass(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    infiniband.LinkFlapMeasurement
	}{
		{"no outages", outages()},
		{"all below window", outages(10, 29)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := linkflapCatalog().Evaluate("rdma_linkflap_fatal", tc.m)
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped || v.Severity != health.Pass {
				t.Fatalf("skipped=%v severity=%v, want evaluated PASS", v.Skipped, v.Severity)
			}
			if !v.RecommendedAction.Equal(health.NoAction) {
				t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
			}
		})
	}
}

// A single outage at or above the 30s default window trips the default
// fatal_flap_threshold of 1.
func TestRDMALinkFlapFatal_OneFatalOutage(t *testing.T) {
	for _, secs := range []int64{30, 114} { // boundary and well over
		v, err := linkflapCatalog().Evaluate("rdma_linkflap_fatal", outages(secs))
		if err != nil {
			t.Fatal(err)
		}
		if v.Skipped || v.Severity != health.Critical {
			t.Fatalf("outage %ds: skipped=%v severity=%v, want CRITICAL", secs, v.Skipped, v.Severity)
		}
		if !v.RecommendedAction.Equal(health.GenericDrain()) {
			t.Errorf("outage %ds: RecommendedAction = %v, want check_network drain", secs, v.RecommendedAction)
		}
	}
}

func TestRDMALinkFlapFatal(t *testing.T) {
	tests := []struct {
		name           string
		cat            func() *catalog.Catalog
		measurement    infiniband.LinkFlapMeasurement
		wantSeverity   health.Severity
		wantSkipped    bool
		wantSkipReason health.SkipReason
	}{
		{
			// Raising fatal_flap_threshold tolerates a lone fatal outage.
			name:         "threshold override tolerates one fatal outage",
			cat:          func() *catalog.Catalog { return linkflapCatalog(thresholdOverride("fatal_flap_threshold", int64(2))) },
			measurement:  outages(114),
			wantSeverity: health.Pass,
		},
		{
			// ...but still fails once the count reaches the new threshold.
			name:         "threshold override still fails at the new threshold",
			cat:          func() *catalog.Catalog { return linkflapCatalog(thresholdOverride("fatal_flap_threshold", int64(2))) },
			measurement:  outages(114, 200),
			wantSeverity: health.Critical,
		},
		{
			// Lowering min_outage_seconds makes shorter outages count as fatal.
			name:         "window override makes a short outage fatal",
			cat:          func() *catalog.Catalog { return linkflapCatalog(thresholdOverride("min_outage_seconds", int64(5))) },
			measurement:  outages(10),
			wantSeverity: health.Critical,
		},
		{
			// A journal read that failed entirely must fail the check, not
			// pass it: the required signal is unavailable.
			name:         "errored journal forces critical",
			cat:          func() *catalog.Catalog { return linkflapCatalog() },
			measurement:  infiniband.ErroredLinkFlap("boot", errors.New("journal unreadable"), infiniband.MeasurementSignals()...),
			wantSeverity: health.Critical,
		},
		{
			// The check is scoped to GB200/GB300: on another architecture it
			// skips rather than evaluating, even when a fatal outage is present.
			name: "skips off architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("A100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    outages(114),
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("rdma_linkflap_fatal", tt.measurement)
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped != tt.wantSkipped || v.SkipReason != tt.wantSkipReason {
				t.Fatalf("skipped=%v reason=%v, want skipped=%v reason=%v", v.Skipped, v.SkipReason, tt.wantSkipped, tt.wantSkipReason)
			}
			if tt.wantSkipped {
				return
			}
			if v.Severity != tt.wantSeverity {
				t.Fatalf("severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
		})
	}
}
