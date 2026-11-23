// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func TestDemonstrationHandlerDoesNotClaimHardwareHealth(t *testing.T) {
	verdict, err := demonstrationHandler(context.Background(), &healthv1.EvaluateRequest{NodeId: proto.String("node-a")})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.GetOverallSeverity() != healthv1.Severity_UNKNOWN {
		t.Fatal("demonstration without hardware must return UNKNOWN")
	}
	if verdict.GetNodeId() != "node-a" {
		t.Fatalf("node ID = %q", verdict.GetNodeId())
	}
	if len(verdict.GetCheckResults()) != 1 || len(verdict.GetCheckResults()[0].GetMessages()) == 0 {
		t.Fatal("verdict must explain why it is UNKNOWN")
	}
	if verdict.GetTraceId() == "" || verdict.GetCheckResults()[0].GetTraceId() != verdict.GetTraceId() {
		t.Fatal("trace IDs do not correlate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, canceledErr := demonstrationHandler(ctx, &healthv1.EvaluateRequest{}); !errors.Is(canceledErr, context.Canceled) {
		t.Fatalf("canceled request = %v", canceledErr)
	}
}
