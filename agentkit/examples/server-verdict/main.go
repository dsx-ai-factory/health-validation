// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// server-verdict demonstrates the transport's host-supplied handler seam.
// It deliberately returns UNKNOWN without collecting hardware measurements.
// Use health-validation serve for the built-in catalog and real node collection.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/agentkit/server"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func main() {
	address := flag.String("address", "127.0.0.1:8080", "shared gRPC and HTTP address")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := server.ServeContext(ctx, *address, demonstrationHandler); err != nil {
		log.Print(err)
		os.Exit(2)
	}
}

func demonstrationHandler(ctx context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodeID := request.GetNodeId()
	if nodeID == "" {
		nodeID = "example-node"
	}
	traceID := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return &healthv1.NodeVerdict{
		SchemaVersion: proto.String("0.4"), CatalogVersion: proto.String("example"), Timestamp: &now,
		NodeId: &nodeID, TraceId: &traceID, OverallSeverity: healthv1.Severity_UNKNOWN.Enum(),
		SeverityByDomain: map[string]healthv1.Severity{"node": healthv1.Severity_UNKNOWN},
		CheckResults: []*healthv1.CheckVerdict{{
			Timestamp: &now, CheckId: proto.String("example_source"), SpanId: proto.String(uuid.NewString()), TraceId: &traceID,
			Severity: healthv1.Severity_UNKNOWN.Enum(), Category: healthv1.Category_node.Enum(), ConditionScope: healthv1.ConditionScope_NODE.Enum(),
			MaturityLevel: healthv1.MaturityLevel_experimental.Enum(), Skipped: proto.Bool(false), NvidiaEndorsed: proto.Bool(false),
			Messages: []string{"Demonstration handler: no hardware measurements were collected. Supply a host evaluator for real verdicts."},
		}},
	}, nil
}
