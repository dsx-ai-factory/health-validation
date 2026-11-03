// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

type evaluationRequest struct {
	Cadence          *health.SchedulingCadence
	NodeID           string
	ExecutionContext health.ExecutionContext
}

// evaluateOnce owns only per-request state. Its caller owns backend and source
// lifetime, output, and metrics publication after the verdict is delivered.
func (a *App) evaluateOnce(ctx context.Context, cfg *Config, request evaluationRequest, xidJournal collect.Sensor) (health.NodeVerdict, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return health.NodeVerdict{}, contextErr
	}
	cycle, err := newCycle(collectionTimeout(cfg))
	if err != nil {
		return health.NodeVerdict{}, err
	}
	arch, discoveryErr := gpuinventory.Architecture(a.host.gpuInventory().Snapshot(ctx, cycle))
	if contextErr := ctx.Err(); contextErr != nil {
		return health.NodeVerdict{}, contextErr
	}
	if discoveryErr != nil {
		slog.Warn("architecture discovery failed", "err", discoveryErr)
		verdict := discoveryFailureVerdict(discoveryErr)
		verdict.NodeID = request.NodeID
		return verdict, nil
	}
	cat, timeout, err := buildCatalog(cfg, a.customChecks, arch, request.ExecutionContext)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{Cadence: request.Cadence})
	if err != nil {
		return health.NodeVerdict{}, fmt.Errorf("select runnable checks: %w", err)
	}
	// All views are reset before any measurements, including caller-provided
	// probe backends that own a separate NVML connection.
	for _, backend := range []any{a.host.gpuBackend(), a.host.nvlinkBackend(), a.gpu, a.node, a.nvlink} {
		if backend, ok := backend.(interface{ BeginCycle() }); ok {
			backend.BeginCycle()
		}
	}
	sensors, err := a.sensorsContext(ctx, cycle, requiredFor(runnable), xidJournal)
	if err != nil {
		return health.NodeVerdict{}, fmt.Errorf("register collect sensors: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return health.NodeVerdict{}, contextErr
	}
	verdict, err := toolkit.CollectWithContext(ctx, cat, toolkit.CollectionOptions{
		Cadence: request.Cadence,
		Timeout: timeout,
	}, sensors...)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	verdict.NodeID = request.NodeID
	return verdict, nil
}
