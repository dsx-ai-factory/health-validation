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

// GPUMetrics pairs one GPU's identity with its requested metric fields.
type GPUMetrics struct {
	Fields   map[string]FieldValue
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
	// Metrics contains per-GPU metric results in GPU discovery order.
	Metrics []GPUMetrics
}

// collectionReader binds one collection round (Cycle) to a client session.
// The native-call gate protects session and admissionError.
type collectionReader struct {
	source  *Source
	cycle   *collect.Cycle
	session *clientSession
	// admissionError retains failure to obtain this collection's initial session.
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
		// Resolve identities first so a later metric timeout cannot strip labels
		// from values already collected for other GPUs.
		identities, result.DiscoveryError = reader.gpuIdentities(ctx)
		for _, identity := range identities {
			result.Metrics = append(result.Metrics, GPUMetrics{
				Identity: identity,
				Fields:   reader.readGPUMetrics(ctx, identity.Index, request.Metrics),
			})
		}
	}
	if request.Health {
		result.HealthReport, result.HealthError = reader.readHealth(ctx)
	}
	if !request.Inventory {
		return result
	}
	// Reuse metric discovery for inventory. Without metrics, discover after
	// health so an inventory discovery failure cannot block the native report.
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
