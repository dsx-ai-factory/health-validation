// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// RDMALinkFlapFatal detects "fatal" RDMA link flaps: an mlx5 RDMA
// interface that went down and took at least min_outage_seconds to come
// back up. Despite the name, the fatal signal is a single prolonged
// outage, not rapid flapping — a link that stays down long enough to
// disrupt collectives. The check fails CRITICAL once the number of such
// outages reaches fatal_flap_threshold.
//
// It consumes a [LinkFlapMeasurement] from the journal link-flap sensor
// and counts outage_durations at or above min_outage_seconds. Both knobs
// are operator-overridable.
func RDMALinkFlapFatal() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "rdma_linkflap_fatal",
		Name:        "RDMA Link Flap (Fatal)",
		Description: "Detects mlx5 RDMA interface link outages that lasted at least min_outage_seconds, parsed from the system journal. Fails CRITICAL once the count of such outages reaches fatal_flap_threshold.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{SignalOutageDurations},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.ClusterScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"min_outage_seconds": {
				Value:       int64(30),
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(0),
				Category:    health.DefaultThreshold,
				Description: "Minimum down→up outage length, in seconds, for an RDMA link transition to count as a fatal flap. Override per fleet.",
				Unit:        "seconds",
			},
			"fatal_flap_threshold": {
				Value:     int64(1),
				ValueType: health.ThresholdInt64,
				// At 0 the rule is unconditionally true: a count is always >= 0.
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Number of outages at or above min_outage_seconds that fails the check. Override per fleet.",
				Unit:        "count",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "size(outage_durations.filter(d, d >= min_outage_seconds)) >= fatal_flap_threshold",
				Severity:   health.Critical,
				Message:    "RDMA link outage(s) lasting at least ${min_outage_seconds}s reached the fatal-flap threshold of ${fatal_flap_threshold}. See flap_events for the affected interfaces.",
			},
		},
	}
}
