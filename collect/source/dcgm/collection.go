// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// CollectionRequest selects DCGM outputs. An empty request performs no native work.
type CollectionRequest struct {
	// Metrics selects previously registered metric signal IDs.
	Metrics []string
	// Health reads the source's registered union of health systems.
	Health bool
	// Inventory requests a GPU inventory independently of metrics and health.
	Inventory bool
}

// CollectedGPU pairs one GPU's identity with its requested metrics.
type CollectedGPU struct {
	Metrics  map[string]FieldValue
	Identity GPUIdentityEntry
}

// CollectionResult preserves available outputs and their collection failures.
type CollectionResult struct {
	// DiscoveryError applies to the GPU discovery shared by metrics and inventory.
	DiscoveryError *observation.CollectionError
	// HealthError describes why a requested health report was unavailable.
	HealthError *observation.CollectionError
	// HealthReport may contain native findings even when identity enrichment fails.
	HealthReport *observation.DCGMHealthReport
	// Inventory includes a failure snapshot when requested discovery fails.
	Inventory *observation.GPUInventorySnapshot
	// GPUs contains metric results in GPU discovery order.
	GPUs []CollectedGPU
}

type collectionReader struct {
	source *Source
	cycle  *collect.Cycle
	// The source's native-call gate protects session and admissionError.
	session        *clientSession
	admissionError error
}

// newCollectionReader prepares an unbound reader without opening DCGM.
func (source *Source) newCollectionReader(cycle *collect.Cycle) *collectionReader {
	return &collectionReader{source: source, cycle: cycle}
}

// Collect reads the requested outputs from one DCGM session. A failed or
// replaced session leaves completed results usable and stops later native reads.
func (source *Source) Collect(ctx context.Context, cycle *collect.Cycle, request CollectionRequest) CollectionResult {
	reader := source.newCollectionReader(cycle)
	var result CollectionResult
	var identities []GPUIdentityEntry
	if len(request.Metrics) > 0 {
		identities, result.DiscoveryError = reader.gpuIdentities(ctx)
		for _, identity := range identities {
			result.GPUs = append(result.GPUs, CollectedGPU{
				Identity: identity,
				Metrics:  reader.readGPUMetrics(ctx, identity.Index, request.Metrics),
			})
		}
	}
	if request.Health {
		result.HealthReport, result.HealthError = reader.readHealth(ctx)
	}
	if !request.Inventory {
		return result
	}
	// Inventory-only discovery follows any selected health read.
	if len(request.Metrics) == 0 {
		identities, result.DiscoveryError = reader.gpuIdentities(ctx)
	}
	if result.DiscoveryError != nil {
		result.Inventory = &observation.GPUInventorySnapshot{
			Source: source.ID(), CollectedAt: timestamppb.Now(),
			Outcome: &observation.GPUInventorySnapshot_CollectionError{CollectionError: result.DiscoveryError},
		}
	} else {
		result.Inventory = reader.gpuInventory(ctx, identities)
	}
	return result
}
