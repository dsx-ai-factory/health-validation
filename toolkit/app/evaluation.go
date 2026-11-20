// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

type evaluationRequest struct {
	xidJournal       collect.Sensor
	xidClaim         *boundedXidClaim
	Cadence          *health.SchedulingCadence
	NodeID           string
	ExecutionContext health.ExecutionContext
	audit            bool
}

type boundedXidClaim struct {
	sensor collect.Sensor
	index  int
}

// evaluateOnce owns only per-request state. Its caller owns backend and source
// lifetime, output, and metrics publication after the verdict is delivered.
func (a *App) evaluateOnce(ctx context.Context, cfg *Config, request evaluationRequest) (health.NodeVerdict, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return health.NodeVerdict{}, contextErr
	}
	cycle, err := newCycle(collectionTimeout(cfg))
	if err != nil {
		return health.NodeVerdict{}, err
	}
	inventory := a.host.gpuInventory()
	snapshot := inventory.Snapshot(ctx, cycle)
	arch, discoveryErr := gpuinventory.Architecture(snapshot)
	if contextErr := ctx.Err(); contextErr != nil {
		return health.NodeVerdict{}, contextErr
	}
	if discoveryErr != nil {
		slog.Warn("architecture discovery failed", "err", discoveryErr)
		verdict := discoveryFailureVerdict(discoveryErr, errors.Is(inventory.Cause(cycle), gpuinventory.ErrSourceBusy))
		verdict.NodeID = request.NodeID
		return verdict, nil
	}
	warnUnknownArchitecture(slog.Default(), arch, snapshot)
	driverVersion, ok := gpuinventory.DriverVersion(snapshot)
	if !ok {
		slog.Warn("driver version unreadable; continuing without it")
	}
	cat, timeout, err := buildCatalog(cfg, a.customChecks, arch, driverVersion, request.ExecutionContext)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	if request.audit {
		if auditErr := a.auditOverrides(cat); auditErr != nil {
			return health.NodeVerdict{}, fmt.Errorf("audit threshold overrides: %w", auditErr)
		}
	}
	runnable, err := cat.RunnableChecks(catalog.EvaluationOptions{Cadence: request.Cadence})
	if err != nil {
		return health.NodeVerdict{}, fmt.Errorf("select runnable checks: %w", err)
	}
	collectionApp := a
	if claim := request.xidClaim; claim != nil {
		scoped := *a
		scoped.collectSources = slices.Clone(a.collectSources)
		scoped.collectSources[claim.index].sensor = claim.sensor
		collectionApp = &scoped
	}
	sensors, err := collectionApp.sensorsContext(ctx, cycle, requiredFor(runnable), request.xidJournal)
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
