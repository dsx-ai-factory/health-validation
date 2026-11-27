// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"math"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/catalogview"
	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/output"
)

// Empty is a tool's input when it takes none.
type Empty struct{}

// ListInput is hv_list_checks's input.
type ListInput struct {
	Category     string `json:"category,omitempty" jsonschema:"only checks in this domain, default ANY"`
	Architecture string `json:"architecture,omitempty" jsonschema:"only checks that run on this GPU architecture, default ANY"`
	Cadence      string `json:"cadence,omitempty" jsonschema:"only checks that run in this scheduling window, default ANY"`
	Maturity     string `json:"maturity,omitempty" jsonschema:"only stable or only experimental checks, default ANY"`
	Page         int    `json:"page,omitempty" jsonschema:"which page of 25 checks, default 1"`
}

// DescribeInput is hv_describe_check's input.
type DescribeInput struct {
	CheckID string `json:"check_id" jsonschema:"the check to describe; hv_list_checks lists them"`
}

// RemediationInput is hv_explain_remediation's input.
type RemediationInput struct {
	Action           string `json:"action" jsonschema:"the remediation step to explain, as a verdict names it"`
	OperationalState string `json:"operational_state,omitempty" jsonschema:"only checks recommending the step with this node state, default ANY"`
}

// VerdictInput is hv_get_verdict's input.
type VerdictInput struct {
	Cadence            string `json:"cadence,omitempty" jsonschema:"run only the checks for this scheduling window, default ALL (every registered check)"`
	Detail             string `json:"detail,omitempty" jsonschema:"summary (default), failing (adds entities and messages) or full (adds skipped and passed checks)"`
	XidLookbackMinutes int    `json:"xid_lookback_minutes,omitempty" jsonschema:"read Xid events from this many minutes ago; 0 (default) keeps serve's own window"`
}

// StuckCall is a source call in serve that has not returned.
type StuckCall struct {
	Source string `json:"source" jsonschema:"fenced text from serve"`
	Since  string `json:"since"`
}

// StatusOutput is hv_service_status's result.
type StatusOutput struct {
	ServeAddress               string      `json:"serve_address"`
	Liveness                   string      `json:"liveness" jsonschema:"ok, unhealthy (a source call is stuck), not_available (serve has no /healthz) or unreachable"`
	Note                       string      `json:"note"`
	Error                      string      `json:"error,omitempty"`
	CatalogVersion             string      `json:"catalog_version"`
	SchemaVersion              string      `json:"schema_version"`
	FenceMarker                string      `json:"fence_marker,omitempty"`
	StuckCalls                 []StuckCall `json:"stuck_calls"`
	EvaluateIntervalSeconds    int         `json:"evaluate_interval_seconds"`
	EvaluateAvailableInSeconds int         `json:"evaluate_available_in_seconds" jsonschema:"0 when hv_get_verdict may run now"`
	Reachable                  bool        `json:"reachable"`
	EvaluateEnabled            bool        `json:"evaluate_enabled"`
	EvaluateRunning            bool        `json:"evaluate_running"`
	OwnerCheck                 bool        `json:"owner_check" jsonschema:"true when the socket owner is checked before each call"`
	StuckCallsTruncated        bool        `json:"stuck_calls_truncated"`
}

const statusNote = "Liveness says whether the serve process answers and whether a source call is stuck. " +
	"It is not node health: hv_get_verdict evaluates the node."

func (s *Server) register() {
	statusTool := &mcp.Tool{
		Name: "hv_service_status", Title: "Service status",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Service status",
			ReadOnlyHint: true, DestructiveHint: pointer(false), IdempotentHint: true, OpenWorldHint: pointer(false),
		},
		Description: "Check whether the local health-validation serve process answers, through its /healthz " +
			"endpoint, and list source calls stuck inside it. Also says whether hv_get_verdict is enabled, " +
			"when it may next run, and which catalog and schema versions this server was built with. " +
			"Liveness is not node health; use hv_get_verdict for that. Read only; starts no collection.",
	}
	statusTool.InputSchema = schemaFor[Empty](nil)
	register(s, statusTool, s.handleStatus)

	listTool := &mcp.Tool{
		Name: "hv_list_checks", Title: "List checks",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List checks",
			ReadOnlyHint: true, DestructiveHint: pointer(false), IdempotentHint: true, OpenWorldHint: pointer(false),
		},
		Description: "List the checks in the health-validation catalog compiled into this server, 25 per page, " +
			"sorted by ID, with each check's domain, cadence, architectures, contexts and maturity. Filter by " +
			"category, architecture, cadence and maturity. Use hv_describe_check for one check's rules and " +
			"thresholds. Reads no node; a node's own config may disable some checks.",
	}
	listTool.InputSchema = schemaFor[ListInput](func(properties map[string]*jsonschema.Schema) {
		properties["category"].Enum = enum([]string{catalogview.Any}, catalogview.Categories)
		properties["category"].Default = defaultValue(catalogview.Any)
		properties["architecture"].Enum = enum([]string{catalogview.Any}, catalogview.Architectures)
		properties["architecture"].Default = defaultValue(catalogview.Any)
		properties["cadence"].Enum = enum([]string{catalogview.Any}, catalogview.Cadences())
		properties["cadence"].Default = defaultValue(catalogview.Any)
		properties["maturity"].Enum = enum([]string{catalogview.Any}, catalogview.Maturities())
		properties["maturity"].Default = defaultValue(catalogview.Any)
		properties["page"].Minimum = pointer(1.0)
		properties["page"].Maximum = pointer(float64(maxPage))
		properties["page"].Default = defaultValue(1)
	})
	register(s, listTool, func(_ context.Context, _ *mcp.CallToolRequest, in ListInput) (
		*mcp.CallToolResult, *catalogview.Listing, error,
	) {
		listing, err := catalogview.List(catalogview.Filter{
			Category: in.Category, Architecture: in.Architecture, Cadence: in.Cadence, Maturity: in.Maturity,
		}, in.Page)

		return nil, listing, err
	})

	describeTool := &mcp.Tool{
		Name: "hv_describe_check", Title: "Describe a check",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Describe a check",
			ReadOnlyHint: true, DestructiveHint: pointer(false), IdempotentHint: true, OpenWorldHint: pointer(false),
		},
		Description: "Return one check's full definition from the catalog compiled into this server: what it " +
			"detects, the signals it reads, each rule's severity, CEL expression and message template, each " +
			"threshold's type, default, per-architecture values, bounds and whether operators may override it " +
			"(FIXED, DEFAULT, ADJUSTABLE), its dependencies and recommended action. Capped at 16 KiB. " +
			"Reads no node.",
	}
	describeTool.InputSchema = schemaFor[DescribeInput](func(properties map[string]*jsonschema.Schema) {
		properties["check_id"].Enum = enum(catalogview.CheckIDEnum)
	})
	register(s, describeTool, func(_ context.Context, _ *mcp.CallToolRequest, in DescribeInput) (
		*mcp.CallToolResult, *catalogview.Description, error,
	) {
		description, err := catalogview.Describe(in.CheckID)

		return nil, description, err
	})

	remediationTool := &mcp.Tool{
		Name: "hv_explain_remediation", Title: "Explain a remediation step",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Explain a remediation step",
			ReadOnlyHint: true, DestructiveHint: pointer(false), IdempotentHint: true, OpenWorldHint: pointer(false),
		},
		Description: "Explain what one remediation step in a verdict means, such as reset_gpu or hold, and list " +
			"up to 30 checks in the compiled-in catalog that recommend it, optionally only with a given node " +
			"state. Neither health-validation nor this server performs any step. Reads no node.",
	}
	remediationTool.InputSchema = schemaFor[RemediationInput](func(properties map[string]*jsonschema.Schema) {
		properties["action"].Enum = enum(catalogview.RemediationActions)
		properties["operational_state"].Enum = enum([]string{catalogview.Any}, catalogview.OperationalStates())
		properties["operational_state"].Default = defaultValue(catalogview.Any)
	})
	register(s, remediationTool, func(_ context.Context, _ *mcp.CallToolRequest, in RemediationInput) (
		*mcp.CallToolResult, *catalogview.Remediation, error,
	) {
		remediation, err := catalogview.Explain(in.Action, in.OperationalState)

		return nil, remediation, err
	})

	if !s.config.AllowEvaluate {
		return
	}

	verdictTool := &mcp.Tool{
		Name: "hv_get_verdict", Title: "Get a node verdict",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get a node verdict",
			ReadOnlyHint: true, DestructiveHint: pointer(false), IdempotentHint: false, OpenWorldHint: pointer(false),
		},
		Description: "Ask the local health-validation serve process to evaluate this node now and return a " +
			"decision: scheduling_advice (READY, READY_LOW_COVERAGE, MONITOR, DRAIN, RETRY_COLLECTION or " +
			"NO_VERDICT), overall severity, node state and ordered remediation steps, plus coverage, which " +
			"says how many GPU, NVLink and Xid checks actually ran. detail=failing adds the failing checks' " +
			"entities and fenced messages; detail=full adds skipped and passed checks, capped at 64 KiB. " +
			"It starts real collection on the node, so it is rate limited and a second call while one runs " +
			"is refused. It changes nothing on the node. Use hv_service_status first if serve may be down.",
	}
	verdictTool.InputSchema = schemaFor[VerdictInput](func(properties map[string]*jsonschema.Schema) {
		properties["cadence"].Enum = enum([]string{cadenceAll}, catalogview.Cadences())
		properties["cadence"].Default = defaultValue(cadenceAll)
		properties["detail"].Enum = enum(details)
		properties["detail"].Default = defaultValue(detailSummary)
		properties["xid_lookback_minutes"].Minimum = pointer(0.0)
		properties["xid_lookback_minutes"].Maximum = pointer(float64(maxLookbackMinutes))
		properties["xid_lookback_minutes"].Default = defaultValue(0)
	})
	register(s, verdictTool, s.handleVerdict)
}

func (s *Server) handleStatus(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (
	*mcp.CallToolResult, *StatusOutput, error,
) {
	config := s.client.Config()
	out := &StatusOutput{
		ServeAddress: config.Address(), Note: statusNote, StuckCalls: []StuckCall{},
		CatalogVersion: catalogview.Version(), SchemaVersion: catalogview.SchemaVersion(),
		EvaluateEnabled:         s.config.AllowEvaluate,
		EvaluateIntervalSeconds: int(s.config.EvaluateInterval.Seconds()),
		OwnerCheck:              config.OwnerUID != nil,
	}

	if s.config.AllowEvaluate {
		out.EvaluateRunning = s.running.Load()
		out.EvaluateAvailableInSeconds = s.evaluateWait()
	}

	liveness, err := s.client.Liveness(ctx)
	if err != nil {
		out.Liveness = "unreachable"
		out.Error = callerError("hv_service_status", err).Error()

		return nil, out, nil
	}

	out.Reachable, out.Liveness = true, liveness.Status

	if len(liveness.StuckCalls) == 0 {
		return nil, out, nil
	}

	fence, err := output.NewFence()
	if err != nil {
		return nil, nil, callerError("hv_service_status", err)
	}

	out.FenceMarker = fence.Marker()

	for _, call := range liveness.StuckCalls {
		if len(out.StuckCalls) == output.MaxStuckCalls {
			out.StuckCallsTruncated = true
			break
		}

		source, _ := fence.Wrap(call.Source, output.MaxSourceBytes)
		out.StuckCalls = append(out.StuckCalls, StuckCall{Source: source, Since: cleanTime(call.Since)})
	}

	return nil, out, nil
}

// evaluateWait is how many seconds until the evaluate limiter allows a call.
func (s *Server) evaluateWait() int {
	missing := 1 - s.evaluate.Tokens()
	if missing <= 0 {
		return 0
	}

	return int(math.Ceil(missing / float64(s.evaluate.Limit())))
}

func cleanTime(text string) string {
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return ""
	}

	return parsed.UTC().Format(time.RFC3339)
}
