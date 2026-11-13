// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package toolkit provides the sensor abstraction partners use to feed
// measurements into the catalog and produce a node verdict.
//
// A [Sensor] collects measurements from a hardware domain, either per observed
// entity or host-wide. [Collect] runs one or more sensors, hands their
// measurements to [catalog.Catalog.EvaluateAll], and returns the aggregated
// [health.NodeVerdict]. Use [CollectWithOptions] for a per-run cadence or
// collection timeout; zero-value [CollectionOptions] uses the defaults.
//
// # Sensors
//
// The stock CLI builds its sensors from collect sensors through the bridge in
// toolkit/app; see that package's WithCollect and WithCollectAdapters. Any
// type implementing [Sensor] can be passed to [Collect] directly; the
// collectSensor example wraps a collect sensor that way:
//
//	cat := catalog.New(
//	    catalog.WithArchitecture("GB200"),
//	    catalog.WithExecutionContext(health.OperatorInfra),
//	)
//	nv, err := toolkit.Collect(cat, mySensor)
//	if err != nil { /* handle */ }
//	fmt.Println(nv.OverallSeverity)
package toolkit
