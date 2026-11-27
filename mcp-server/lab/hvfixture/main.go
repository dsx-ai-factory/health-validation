// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command hvfixture runs health-validation's real verdict server (HTTP and gRPC on one
// port, request admission, /healthz) in front of hv's real catalog, evaluating synthetic
// measurements for one scenario. It stands in for `health-validation serve` on hosts
// without the GPUs a scenario needs. Its verdicts are labelled synthetic wherever they
// are recorded.
//
// It writes one line to stdout, "listening on <address>", and JSON events to stderr.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dsx-ai-factory/health-validation/agentkit/verdictserver"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
)

const (
	defaultNodeID = "hvfixture-node"
	maxHold       = 10 * time.Minute
)

type settings struct {
	address  string
	scenario string
	hold     time.Duration
}

func main() {
	config, err := parse(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, config, os.Stdout, logger); err != nil {
		logger.Error("stopped", "error", err)
		os.Exit(1)
	}
}

func parse(args []string, stderr io.Writer) (settings, error) {
	var config settings

	flags := flag.NewFlagSet("hvfixture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&config.address, "address", "127.0.0.1:0", "numeric loopback address; port 0 picks a free port")
	flags.StringVar(&config.scenario, "scenario", "", "one of: "+strings.Join(scenarioNames(), ", "))
	flags.DurationVar(&config.hold, "hold", 0, "keep each evaluation open this long before answering, up to 10m")

	if err := flags.Parse(args); err != nil {
		return config, err
	}

	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q", flags.Arg(0))
		_, _ = fmt.Fprintln(stderr, err)

		return config, err
	}

	if err := validate(config); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return config, err
	}

	return config, nil
}

func validate(config settings) error {
	if _, ok := scenarios[config.scenario]; !ok {
		return fmt.Errorf("--scenario must be one of: %s", strings.Join(scenarioNames(), ", "))
	}

	if config.hold < 0 || config.hold > maxHold {
		return fmt.Errorf("--hold must be from 0 to %s", maxHold)
	}

	// Like serve, only a numeric loopback address.
	address, err := netip.ParseAddrPort(config.address)
	if err != nil || !address.Addr().IsLoopback() {
		return fmt.Errorf("--address must be a numeric loopback address and port, such as 127.0.0.1:8080")
	}

	return nil
}

func serve(ctx context.Context, config settings, stdout io.Writer, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	chosen := scenarios[config.scenario]

	logger.Info("started", "scenario", config.scenario, "claim", chosen.claim, "hold", config.hold.String(),
		"measurements", "synthetic")

	if _, err := fmt.Fprintf(stdout, "listening on %s\n", listener.Addr()); err != nil {
		_ = listener.Close()
		return err
	}

	handler := func(ctx context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		return evaluate(ctx, config, chosen, request, logger)
	}

	return verdictserver.ServeListener(ctx, listener, handler, verdictserver.WithLiveness(chosen.liveness))
}

func evaluate(ctx context.Context, config settings, chosen scenario, request *healthv1.EvaluateRequest,
	logger *slog.Logger,
) (*healthv1.NodeVerdict, error) {
	start := time.Now()

	logger.Info("evaluate_received", "cadence", request.GetCadence().String(), "has_cadence", request.Cadence != nil,
		"has_xid_from", request.XidFrom != nil, "xid_from", request.GetXidFrom(), "has_xid_to", request.XidTo != nil,
		"has_node_id", request.NodeId != nil, "has_execution_context", request.ExecutionContext != nil)

	var cadence *health.SchedulingCadence

	if request.Cadence != nil {
		parsed, err := health.SchedulingCadenceFromName(request.GetCadence().String())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid cadence: %v", err)
		}

		cadence = &parsed
	}

	if config.hold > 0 {
		timer := time.NewTimer(config.hold)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			logger.Info("evaluate_cancelled", "seconds", time.Since(start).Seconds(), "cause", ctx.Err().Error())
			return nil, ctx.Err()
		}
	}

	verdict, err := chosen.evaluate(cadence)
	if err != nil {
		return nil, fmt.Errorf("evaluate scenario: %w", err)
	}

	verdict.NodeID = defaultNodeID
	if request.NodeId != nil {
		verdict.NodeID = request.GetNodeId()
	}

	wire, err := verdictproto.ToProto(verdict)
	if err != nil {
		return nil, fmt.Errorf("convert verdict: %w", err)
	}

	logger.Info("evaluate_answered", "overall_severity", wire.GetOverallSeverity().String(),
		"checks_run", len(wire.GetCheckResults()), "checks_skipped", len(wire.GetSkippedChecks()),
		"seconds", time.Since(start).Seconds())

	return wire, nil
}
