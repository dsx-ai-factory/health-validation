// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// server-simple-collect serves one collection cycle per request over gRPC and
// HTTP on a single port. Built-in sensors are registered here as they land
// in collect/sensors. See scan-one-off for consumer-side Xid parsing and
// InfiniBand port-down reporting from the returned observations.
package main

import (
	"flag"
	"fmt"
	"log"
	"maps"

	"github.com/dsx-ai-factory/health-validation/agentkit/server"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	ibsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/infiniband"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	kmsgmemory "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg/memory"
	kmsgxid "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg/xid"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
)

func main() {
	// Deferred cleanup runs only when run returns; log.Fatal alone would
	// call os.Exit and skip the defers.
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := flag.String("address", "127.0.0.1:18080", "shared gRPC and HTTP address")
	kmsgCapacity := flag.Int("kmsg-capacity", kmsgsource.DefaultStreamCapacity, "maximum kernel records retained in memory")
	flag.Parse()

	// NVML source client: one lifecycle for the process; hosts without NVML
	// get the no-op instance and the sensor reports per-signal errors.
	nvmlInst, err := nvmlsource.New()
	if err != nil {
		return fmt.Errorf("init NVML: %w", err)
	}
	defer func() { _ = nvmlInst.Shutdown() }()

	// DCGM initializes lazily on the first collection. Creating the sensor now
	// registers every field before DCGM creates its metric watch.
	dcgmSource := dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})
	defer func() { _ = dcgmSource.Close() }()
	dcgmSensor, err := dcgmsensor.New(dcgmSource)
	if err != nil {
		return fmt.Errorf("init DCGM sensor: %w", err)
	}

	kmsgStream, err := kmsgsource.NewStream(*kmsgCapacity)
	if err != nil {
		return fmt.Errorf("start kmsg stream: %w", err)
	}
	defer func() { _ = kmsgStream.Close() }()
	patterns := kmsgmemory.DefaultPatterns()
	maps.Copy(patterns, kmsgxid.DefaultPatterns())
	kmsgSensor, err := kmsgsensor.New(kmsgStream.Read, patterns)
	if err != nil {
		return fmt.Errorf("init kmsg sensor: %w", err)
	}

	collector := server.New()
	collector.Register(nvmlsensor.New(nvmlInst), dcgmSensor, ibsensor.New(nil), kmsgSensor)

	log.Printf("serving plaintext gRPC and HTTP on %s", *address)
	return server.Serve(*address, collector)
}
