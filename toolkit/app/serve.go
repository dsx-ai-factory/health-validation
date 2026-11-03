// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dsx-ai-factory/health-validation/agentkit/server"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
)

func (a *App) serveCommand(args []string, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.serve(ctx, args, stderr)
}

func (a *App) serve(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(a.prog+" serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	address := fs.String("address", "127.0.0.1:8080", "TCP address for the local gRPC and HTTP verdict API")
	configPath := fs.String("config", "", "path to a YAML configuration file")
	nodeID := fs.String("node-id", a.nodeID, "default node identifier")
	cadenceFlag := fs.String("cadence", "", "default cadence window (CONTINUOUS, PROLOG, MAINTENANCE)")
	contextFlag := fs.String("execution-context", health.TenantGuest.String(), "default execution context")
	registerXidTimeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "%s: %v\n", a.prog, err)
		return exitError
	}
	if fs.NArg() != 0 {
		return fail(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*address) == "" {
		return fail(fmt.Errorf("--address must not be empty"))
	}
	nodeIDSet, cadenceSet := a.nodeIDSet, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "node-id" {
			nodeIDSet = true
		}
		if f.Name == "cadence" {
			cadenceSet = true
		}
	})
	resolvedNodeID, err := a.resolveNodeID(*nodeID, nodeIDSet)
	if err != nil {
		return fail(err)
	}
	defaults := evaluationRequest{NodeID: resolvedNodeID}
	defaults.ExecutionContext, err = health.ExecutionContextFromName(strings.ToUpper(strings.TrimSpace(*contextFlag)))
	if err != nil {
		return fail(fmt.Errorf("--execution-context: %w", err))
	}
	if cadenceSet {
		cadence, cadenceErr := health.SchedulingCadenceFromName(strings.ToUpper(strings.TrimSpace(*cadenceFlag)))
		if cadenceErr != nil {
			return fail(fmt.Errorf("--cadence: %w", cadenceErr))
		}
		defaults.Cadence = &cadence
	}
	xidJournal, err := xidJournalFromFlags(fs)
	if err != nil {
		return fail(err)
	}
	cfg, err := loadOptionalConfig(*configPath)
	if err != nil {
		return fail(err)
	}
	if _, _, err := buildCatalog(cfg, a.customChecks, "", defaults.ExecutionContext); err != nil {
		return fail(err)
	}
	agent := a.newVerdictAgent(cfg, defaults, xidJournal)
	defer agent.closeWhenIdle()
	if err := server.ServeContext(ctx, *address, agent.evaluate, server.WithAfterResponse(agent.afterResponse)); err != nil {
		return fail(err)
	}
	return exitOK
}

type verdictAgent struct {
	app      *App
	cfg      *Config
	journal  collect.Sensor
	gate     chan struct{}
	defaults evaluationRequest
}

func (a *App) newVerdictAgent(cfg *Config, defaults evaluationRequest, journal collect.Sensor) *verdictAgent {
	return &verdictAgent{app: a, cfg: cfg, defaults: defaults, journal: journal, gate: make(chan struct{}, 1)}
}

func (agent *verdictAgent) closeWhenIdle() {
	closeBackend := func() {
		if err := agent.app.host.close(); err != nil {
			slog.Warn("backend shutdown failed", "error", err)
		}
		<-agent.gate
	}
	select {
	case agent.gate <- struct{}{}:
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			closeBackend()
		}()
		select {
		case <-closed:
		case <-time.After(time.Second):
			slog.Warn("backend shutdown is still running")
		}
	default:
		// Listener shutdown is bounded even when native collection cannot stop.
		go func() {
			agent.gate <- struct{}{}
			closeBackend()
		}()
	}
}

func (agent *verdictAgent) request(req *healthv1.EvaluateRequest) (evaluationRequest, error) {
	request := agent.defaults
	if req == nil {
		return request, status.Error(codes.InvalidArgument, "evaluate request is required")
	}
	if req.NodeId != nil {
		nodeID := strings.TrimSpace(req.GetNodeId())
		if nodeID == "" {
			return request, status.Error(codes.InvalidArgument, "node_id must not be empty")
		}
		request.NodeID = nodeID
	}
	if req.Cadence != nil {
		cadence, err := health.SchedulingCadenceFromName(req.GetCadence().String())
		if err != nil {
			return request, status.Error(codes.InvalidArgument, err.Error())
		}
		request.Cadence = &cadence
	}
	if req.ExecutionContext != nil {
		value, err := health.ExecutionContextFromName(req.GetExecutionContext().String())
		if err != nil {
			return request, status.Error(codes.InvalidArgument, err.Error())
		}
		request.ExecutionContext = value
	}
	return request, nil
}

func (agent *verdictAgent) evaluate(ctx context.Context, req *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
	request, err := agent.request(req)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	traceID := uuid.NewString()
	// Admission time is included so queued callers have the same bounded budget.
	ctx, cancel := context.WithTimeout(ctx, collectionTimeout(agent.cfg))
	defer cancel()
	select {
	case agent.gate <- struct{}{}:
	case <-ctx.Done():
		slog.Info("verdict request canceled while queued", "request_id", traceID, "node_id", request.NodeID, "duration", time.Since(started), "error", ctx.Err())
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if err := ctx.Err(); err != nil {
		<-agent.gate
		return nil, status.FromContextError(err).Err()
	}
	type result struct {
		err     error
		verdict health.NodeVerdict
	}
	completed := make(chan result, 1)
	tracked, tracker := collect.WithCallTracking(ctx)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				completed <- result{err: fmt.Errorf("evaluation panicked: %v", recovered)}
			}
			// Native calls can outlive their watchdog. Admission and shutdown
			// wait for completion before touching the same backend again.
			tracker.Wait()
			<-agent.gate
		}()
		verdict, err := agent.app.evaluateOnce(tracked, agent.cfg, request, agent.journal)
		completed <- result{verdict: verdict, err: err}
	}()
	select {
	case <-ctx.Done():
		slog.Info("verdict request canceled", "request_id", traceID, "node_id", request.NodeID, "duration", time.Since(started), "error", ctx.Err())
		return nil, status.FromContextError(ctx.Err()).Err()
	case outcome := <-completed:
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		if outcome.err != nil {
			slog.Error("verdict request failed", "request_id", traceID, "node_id", request.NodeID, "duration", time.Since(started), "error", outcome.err)
			return nil, status.Error(codes.Internal, outcome.err.Error())
		}
		verdict, err := verdictproto.ToProto(outcome.verdict)
		if err != nil {
			slog.Error("verdict conversion failed", "trace_id", outcome.verdict.TraceID, "node_id", request.NodeID, "duration", time.Since(started), "error", err)
			return nil, status.Errorf(codes.Internal, "convert verdict: %v", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		slog.Info("verdict request evaluated", "trace_id", outcome.verdict.TraceID, "node_id", request.NodeID, "duration", time.Since(started), "overall_severity", outcome.verdict.OverallSeverity.String())
		return verdict, nil
	}
}

func (agent *verdictAgent) afterResponse(_ context.Context, _ *healthv1.EvaluateRequest, response *healthv1.NodeVerdict) {
	if !agent.cfg.Metrics.Enabled {
		return
	}
	verdict, err := verdictproto.FromProto(response)
	if err != nil {
		slog.Warn("metrics publishing skipped", "error", err)
		return
	}
	publishMetrics(agent.cfg.Metrics, verdict, verdict.NodeID)
}
