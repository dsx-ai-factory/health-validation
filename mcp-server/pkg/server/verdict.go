// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"

	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/catalogview"
	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/hvclient"
	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/output"
)

const (
	cadenceAll    = "ALL"
	detailSummary = "summary"
	detailFailing = "failing"
	detailFull    = "full"

	maxFindings       = 50
	maxMessages       = 4
	maxObserved       = 16
	maxListedChecks   = 200
	MaxVerdictBytes   = 64 << 10
	computeAppsCheck  = "gpu_compute_apps"
	xidCheckPrefix    = "gpu_xid_"
	invalidCheckID    = "[invalid check id]"
	unknownAction     = "[unrecognized action]"
	processCountKey   = "compute_process_count"
	processCmdlineKey = "compute_process_cmdlines"
)

// The scheduling advice values.
const (
	AdviceReady           = "READY"
	AdviceLowCoverage     = "READY_LOW_COVERAGE"
	AdviceMonitor         = "MONITOR"
	AdviceDrain           = "DRAIN"
	AdviceRetryCollection = "RETRY_COLLECTION"
	AdviceNoVerdict       = "NO_VERDICT"
)

var (
	details       = []string{detailSummary, detailFailing, detailFull}
	checkIDFormat = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	versionFormat = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,64}$`)

	adviceText = map[string]string{
		AdviceReady: "Nothing failed among the checks that ran, and GPU or NVLink checks were among " +
			"them. It is a snapshot: evaluate again before each job.",
		AdviceLowCoverage: "Nothing failed, but no GPU or NVLink check ran, so this says almost " +
			"nothing about GPU health. Xid checks only read the journal. Do not read it as healthy.",
		AdviceMonitor: "Keep the node in service and watch the findings.",
		AdviceDrain: "Take the node out of scheduling. The remediation steps are recommendations; " +
			"nothing here performs them.",
		AdviceRetryCollection: "Data could not be collected, which is not a confirmed fault. Do not " +
			"schedule on the node; collect again before any hardware step.",
		AdviceNoVerdict: "No trusted verdict was produced. Do not schedule on the node based on this call.",
	}
)

// Decision is what to do with the node.
type Decision struct {
	SchedulingAdvice   string   `json:"scheduling_advice" jsonschema:"READY, READY_LOW_COVERAGE, MONITOR, DRAIN, RETRY_COLLECTION or NO_VERDICT"`
	Explanation        string   `json:"explanation"`
	OverallSeverity    string   `json:"overall_severity,omitempty"`
	OperationalState   string   `json:"operational_state,omitempty"`
	RemediationActions []string `json:"remediation_actions" jsonschema:"ordered steps; hv_explain_remediation explains each"`
}

// Coverage says how much of the node was actually checked.
type Coverage struct {
	SkippedByReason   map[string]int `json:"skipped_by_reason"`
	LowCoverageReason string         `json:"low_coverage_reason,omitempty"`
	ChecksRun         int            `json:"checks_run"`
	ChecksSkipped     int            `json:"checks_skipped"`
	GPUChecksRun      int            `json:"gpu_checks_run"`
	NVLinkChecksRun   int            `json:"nvlink_checks_run"`
	XidChecksRun      int            `json:"xid_checks_run"`
	LowCoverage       bool           `json:"low_coverage"`
}

// Finding is one non-PASS check result.
type Finding struct {
	ObservedValues       map[string]any `json:"observed_values,omitempty"`
	CheckID              string         `json:"check_id"`
	Name                 string         `json:"name,omitempty" jsonschema:"from the compiled-in catalog"`
	Category             string         `json:"category"`
	Severity             string         `json:"severity"`
	OperationalState     string         `json:"operational_state,omitempty"`
	Redacted             string         `json:"redacted,omitempty" jsonschema:"what the server withheld and why"`
	Entities             []string       `json:"entities,omitempty"`
	Messages             []string       `json:"messages,omitempty" jsonschema:"fenced node text, or a sentence the server built"`
	RemediationActions   []string       `json:"remediation_actions,omitempty"`
	UnrecognizedEntities int            `json:"unrecognized_entities,omitempty" jsonschema:"entity names withheld because they did not look like a GPU UUID, PCI address, port or index"`
	FoldedChecks         int            `json:"folded_checks,omitempty" jsonschema:"how many other gpu_xid_* checks could not read the journal either; they are folded into this finding"`
	Truncated            bool           `json:"truncated,omitempty"`
}

// SkippedCheck is a check that did not run.
type SkippedCheck struct {
	CheckID string `json:"check_id"`
	Reason  string `json:"reason"`
}

// RequestEcho repeats the arguments used.
type RequestEcho struct {
	Cadence            string `json:"cadence"`
	Detail             string `json:"detail"`
	XidLookbackMinutes int    `json:"xid_lookback_minutes"`
}

// VerdictOutput is hv_get_verdict's result.
type VerdictOutput struct {
	SeverityByDomain     map[string]string `json:"severity_by_domain,omitempty"`
	Coverage             *Coverage         `json:"coverage,omitempty"`
	Error                string            `json:"error,omitempty"`
	CatalogVersion       string            `json:"catalog_version,omitempty" jsonschema:"the catalog serve evaluated with"`
	ServerCatalogVersion string            `json:"server_catalog_version" jsonschema:"the catalog compiled into this server"`
	SchemaVersion        string            `json:"schema_version,omitempty"`
	Timestamp            string            `json:"timestamp,omitempty"`
	FenceMarker          string            `json:"fence_marker,omitempty"`
	Request              RequestEcho       `json:"request"`
	Findings             []Finding         `json:"findings"`
	Skipped              []SkippedCheck    `json:"skipped,omitempty"`
	Passed               []string          `json:"passed,omitempty"`
	Decision             Decision          `json:"decision"`
	VersionSkew          bool              `json:"version_skew" jsonschema:"true when serve's catalog differs from this server's"`
	Truncated            bool              `json:"truncated" jsonschema:"true when lists were cut to fit the caps"`
}

func (s *Server) handleVerdict(ctx context.Context, _ *mcp.CallToolRequest, in VerdictInput) (
	*mcp.CallToolResult, *VerdictOutput, error,
) {
	start := time.Now()
	echo := echoOf(in)

	if !s.evaluating.TryLock() {
		s.logEvaluate(echo, "refused_busy", start, "")
		return nil, nil, errors.New("refused: an evaluation is already running; this server does not queue them")
	}
	defer s.evaluating.Unlock()

	s.running.Store(true)
	defer s.running.Store(false)

	if !s.evaluate.Allow() {
		s.logEvaluate(echo, "refused_rate", start, "")
		return nil, nil, fmt.Errorf("refused: at most one evaluation every %d seconds; try again in %d seconds",
			int(s.config.EvaluateInterval.Seconds()), max(1, s.evaluateWait()))
	}

	s.inFlight.Add(1)
	defer s.inFlight.Done()

	request, err := evaluateRequest(echo, time.Now())
	if err != nil {
		s.logEvaluate(echo, "refused", start, "")
		return nil, nil, err
	}

	verdict, err := s.client.Evaluate(ctx, request)
	if err != nil {
		kind := hvclient.KindOf(err)
		s.logEvaluate(echo, outcomeOf(kind), start, string(kind))

		return nil, noVerdict(echo, callerError("hv_get_verdict", err)), nil
	}

	out, err := summarize(verdict, echo)
	if err != nil {
		s.logEvaluate(echo, "no_verdict", start, "summarize")
		return nil, noVerdict(echo, callerError("hv_get_verdict", err)), nil
	}

	s.logEvaluate(echo, "ok", start, "", "advice", out.Decision.SchedulingAdvice)

	return nil, out, nil
}

func echoOf(in VerdictInput) RequestEcho {
	return RequestEcho{
		Cadence: orDefault(in.Cadence, cadenceAll), Detail: orDefault(in.Detail, detailSummary),
		XidLookbackMinutes: in.XidLookbackMinutes,
	}
}

func evaluateRequest(echo RequestEcho, now time.Time) (hvclient.EvaluateRequest, error) {
	var request hvclient.EvaluateRequest

	if echo.Cadence != cadenceAll {
		value, ok := healthv1.SchedulingCadence_value[echo.Cadence]
		if !ok {
			return request, fmt.Errorf("unknown cadence %q", echo.Cadence)
		}

		cadence := healthv1.SchedulingCadence(value)
		request.Cadence = &cadence
	}

	if echo.XidLookbackMinutes > 0 {
		from := now.Add(-time.Duration(echo.XidLookbackMinutes) * time.Minute).Unix()
		request.XidFrom = &from
	}

	return request, nil
}

func outcomeOf(kind hvclient.Kind) string {
	switch kind {
	case hvclient.KindBusy:
		return "busy"
	case hvclient.KindTimeout:
		return "timeout"
	case hvclient.KindCancelled:
		return "cancelled"
	case hvclient.KindUnsupportedSchema:
		return "unsupported"
	case hvclient.KindNotRunning, hvclient.KindUnreachable:
		return "not_running"
	case hvclient.KindWrongOwner:
		return "wrong_owner"
	default:
		return "no_verdict"
	}
}

// logEvaluate writes one JSON line to stderr per evaluation attempt, refusals included.
// Arguments are values the schema allowed, never free text.
func (s *Server) logEvaluate(echo RequestEcho, outcome string, start time.Time, kind string, extra ...any) {
	attributes := []any{
		"tool", "hv_get_verdict",
		"cadence", echo.Cadence, "detail", echo.Detail, "xid_lookback_minutes", echo.XidLookbackMinutes,
		"outcome", outcome, "seconds", time.Since(start).Round(10 * time.Millisecond).Seconds(),
	}
	if kind != "" {
		attributes = append(attributes, "error_kind", kind)
	}

	s.audit.Info("evaluate", append(attributes, extra...)...)
}

func noVerdict(echo RequestEcho, err error) *VerdictOutput {
	return &VerdictOutput{
		Decision: Decision{
			SchedulingAdvice: AdviceNoVerdict, Explanation: adviceText[AdviceNoVerdict], RemediationActions: []string{},
		},
		Error: err.Error(), Request: echo, Findings: []Finding{}, ServerCatalogVersion: catalogview.Version(),
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

// summarize turns a decoded verdict into the tool's answer. Only enum values, numbers
// and text that passed a strict pattern are copied; free text is fenced or replaced.
func summarize(verdict *healthv1.NodeVerdict, echo RequestEcho) (*VerdictOutput, error) {
	fence, err := output.NewFence()
	if err != nil {
		return nil, err
	}

	out := &VerdictOutput{
		Request: echo, Findings: []Finding{}, ServerCatalogVersion: catalogview.Version(),
		SchemaVersion: verdict.GetSchemaVersion(), SeverityByDomain: map[string]string{},
		CatalogVersion: safeVersion(verdict.GetCatalogVersion()), Timestamp: cleanTime(verdict.GetTimestamp()),
	}
	out.VersionSkew = out.CatalogVersion != out.ServerCatalogVersion

	for domain, severity := range verdict.GetSeverityByDomain() {
		out.SeverityByDomain[domain] = severity.String()
	}

	out.Coverage = coverage(verdict)
	out.Decision = decide(verdict, out.Coverage)

	results := slices.Clone(verdict.GetCheckResults())
	slices.SortStableFunc(results, func(a, b *healthv1.CheckVerdict) int {
		if order := severityRank(b.GetSeverity()) - severityRank(a.GetSeverity()); order != 0 {
			return order
		}

		return strings.Compare(a.GetCheckId(), b.GetCheckId())
	})

	// Every gpu_xid_* check is UNKNOWN when the journal cannot be read. One finding
	// stands for them all, so they cannot crowd other findings out of the cap.
	unreadXid := -1

	var unreadEntities []string

	for _, result := range results {
		if result.GetSeverity() == healthv1.Severity_PASS {
			if echo.Detail == detailFull && len(out.Passed) < maxListedChecks {
				out.Passed = append(out.Passed, safeCheckID(result.GetCheckId(), fence))
			}

			continue
		}

		unread := unreadXidResult(result)
		if unread && unreadXid >= 0 {
			out.Findings[unreadXid].FoldedChecks++
			unreadEntities = append(unreadEntities, result.GetAffectedEntities()...)

			continue
		}

		if len(out.Findings) == maxFindings {
			out.Truncated = true
			continue
		}

		out.Findings = append(out.Findings, finding(result, echo.Detail, fence))

		if unread {
			unreadXid = len(out.Findings) - 1
			unreadEntities = slices.Clone(result.GetAffectedEntities())
		}
	}

	if unreadXid >= 0 && echo.Detail != detailSummary {
		folded := &out.Findings[unreadXid]
		folded.Messages = []string{output.XidUnreadSentence(folded.CheckID, folded.FoldedChecks)}
		folded.Entities, folded.UnrecognizedEntities, folded.Truncated = output.Entities(unreadEntities)
	}

	if echo.Detail == detailFull {
		for _, skipped := range verdict.GetSkippedChecks() {
			if len(out.Skipped) == maxListedChecks {
				out.Truncated = true
				break
			}

			out.Skipped = append(out.Skipped, SkippedCheck{
				CheckID: safeCheckID(skipped.GetCheckId(), fence), Reason: skipped.GetSkipReason().String(),
			})
		}
	}

	if echo.Detail != detailSummary || fencedID(out) {
		out.FenceMarker = fence.Marker()
	}

	fitVerdict(out)

	return out, nil
}

func coverage(verdict *healthv1.NodeVerdict) *Coverage {
	covered := &Coverage{
		SkippedByReason: map[string]int{},
		ChecksRun:       len(verdict.GetCheckResults()), ChecksSkipped: len(verdict.GetSkippedChecks()),
	}

	for _, result := range verdict.GetCheckResults() {
		switch result.GetCategory() {
		case healthv1.Category_gpu:
			covered.GPUChecksRun++
		case healthv1.Category_nvlink:
			covered.NVLinkChecksRun++
		case healthv1.Category_xid:
			covered.XidChecksRun++
		default:
		}
	}

	for _, skipped := range verdict.GetSkippedChecks() {
		covered.SkippedByReason[skipped.GetSkipReason().String()]++
	}

	// Xid checks read the journal, not the GPU, so they do not count as coverage.
	if covered.GPUChecksRun+covered.NVLinkChecksRun == 0 {
		covered.LowCoverage = true
		covered.LowCoverageReason = fmt.Sprintf("no GPU or NVLink check ran: %d checks ran (%d of them Xid "+
			"checks, which only read the journal), %d were skipped",
			covered.ChecksRun, covered.XidChecksRun, covered.ChecksSkipped)

		if count := covered.SkippedByReason[healthv1.SkipReason_SIGNAL_UNAVAILABLE_ARCHITECTURE.String()]; count > 0 {
			covered.LowCoverageReason += fmt.Sprintf(", %d of them because the GPU architecture is not one "+
				"they apply to (an unknown architecture skips every GPU check)", count)
		}

		if count := covered.SkippedByReason[healthv1.SkipReason_CADENCE_MISMATCH.String()]; count > 0 {
			covered.LowCoverageReason += fmt.Sprintf(", %d because they do not run at the requested cadence", count)
		}
	}

	return covered
}

func decide(verdict *healthv1.NodeVerdict, covered *Coverage) Decision {
	severity := verdict.GetOverallSeverity()
	state := verdict.GetRecommendedAction().GetOperationalState()
	actions := safeActions(verdict.GetRecommendedAction().GetRemediationActions())

	decision := Decision{
		OverallSeverity: severity.String(), RemediationActions: actions,
	}
	if state != healthv1.OperationalState_OPERATIONAL_STATE_UNSPECIFIED {
		decision.OperationalState = state.String()
	}

	switch {
	case severity == healthv1.Severity_UNKNOWN && len(actions) > 0 && actions[0] == "retry_collection":
		decision.SchedulingAdvice = AdviceRetryCollection
	case severity == healthv1.Severity_CRITICAL, severity == healthv1.Severity_UNKNOWN,
		state == healthv1.OperationalState_drain:
		decision.SchedulingAdvice = AdviceDrain
	case severity == healthv1.Severity_WARNING, state == healthv1.OperationalState_monitor:
		decision.SchedulingAdvice = AdviceMonitor
	case covered.LowCoverage:
		decision.SchedulingAdvice = AdviceLowCoverage
	default:
		decision.SchedulingAdvice = AdviceReady
	}

	decision.Explanation = adviceText[decision.SchedulingAdvice]

	return decision
}

func severityRank(severity healthv1.Severity) int {
	switch severity {
	case healthv1.Severity_CRITICAL:
		return 4
	case healthv1.Severity_UNKNOWN:
		return 3
	case healthv1.Severity_WARNING:
		return 2
	case healthv1.Severity_INFO:
		return 1
	default:
		return 0
	}
}

func finding(result *healthv1.CheckVerdict, detail string, fence *output.Fence) Finding {
	id := safeCheckID(result.GetCheckId(), fence)
	out := Finding{
		CheckID: id, Name: catalogview.CheckName(id),
		Category: result.GetCategory().String(), Severity: result.GetSeverity().String(),
	}

	if detail == detailSummary {
		return out
	}

	action := result.GetRecommendedAction()
	if action.GetOperationalState() != healthv1.OperationalState_OPERATIONAL_STATE_UNSPECIFIED {
		out.OperationalState = action.GetOperationalState().String()
	}

	out.RemediationActions = safeActions(action.GetRemediationActions())

	entities, dropped, cut := output.Entities(result.GetAffectedEntities())
	out.Entities, out.UnrecognizedEntities, out.Truncated = entities, dropped, cut

	observed := result.GetObservedValues()

	unknown := result.GetSeverity() == healthv1.Severity_UNKNOWN

	switch {
	case id == computeAppsCheck && unknown:
		out.Messages = []string{output.ComputeAppsUnreadSentence()}
		out.Redacted = "command lines: the tenant controls them"

		return out
	case id == computeAppsCheck:
		count, pids := processCount(observed), processIDs(result)
		if count >= 0 && int64(len(pids)) > count {
			pids = pids[:count]
		}

		out.Messages = []string{output.ComputeAppsSentence(count, pids)}
		out.Redacted = "command lines: the tenant controls them"

		if detail == detailFull {
			out.ObservedValues = map[string]any{processCountKey: processCount(observed)}
		}

		return out
	case strings.HasPrefix(id, xidCheckPrefix):
		out.Messages = []string{output.XidSentence(id, catalogview.RuleMessage(id, out.Severity))}
		if unknown {
			out.Messages = []string{output.XidUnreadSentence(id, 0)}
		}

		out.Redacted = "journal text, process names and observed values: a tenant can set them"

		return out
	}

	for _, message := range result.GetMessages() {
		if len(out.Messages) == maxMessages {
			out.Truncated = true
			break
		}

		text, cut := fence.Wrap(message, output.MaxMessageBytes)
		out.Messages = append(out.Messages, text)
		out.Truncated = out.Truncated || cut
	}

	if detail == detailFull {
		out.ObservedValues = observedValues(observed, fence, &out.Truncated)
	}

	return out
}

// unreadXidResult is a gpu_xid_* check that could not run because its journal read failed.
func unreadXidResult(result *healthv1.CheckVerdict) bool {
	return result.GetSeverity() == healthv1.Severity_UNKNOWN && strings.HasPrefix(result.GetCheckId(), xidCheckPrefix) &&
		catalogview.KnownCheckID(result.GetCheckId())
}

func processCount(observed map[string]*structpb.Value) int64 {
	value, ok := observed[processCountKey]
	if !ok {
		return -1
	}

	number, ok := value.GetKind().(*structpb.Value_NumberValue)
	if !ok || number.NumberValue < 0 || number.NumberValue > 1e6 {
		return -1
	}

	return int64(number.NumberValue)
}

func processIDs(result *healthv1.CheckVerdict) []int64 {
	text := result.GetObservedValues()[processCmdlineKey].GetStringValue()
	if text == "" {
		text = strings.Join(result.GetMessages(), "\n")
	}

	return output.PIDs(text)
}

// observedValues keeps numbers and booleans, fences strings, and leaves out lists and
// objects, which can hold anything.
func observedValues(observed map[string]*structpb.Value, fence *output.Fence, truncated *bool) map[string]any {
	if len(observed) == 0 {
		return nil
	}

	out := map[string]any{}

	for _, key := range slices.Sorted(maps.Keys(observed)) {
		if !checkIDFormat.MatchString(key) {
			*truncated = true
			continue
		}

		if len(out) == maxObserved {
			*truncated = true
			break
		}

		shown := key
		if !catalogview.KnownSignal(key) {
			shown, _ = fence.Wrap(key, len(key))
		}

		switch value := observed[key].GetKind().(type) {
		case *structpb.Value_NumberValue:
			out[shown] = value.NumberValue
		case *structpb.Value_BoolValue:
			out[shown] = value.BoolValue
		case *structpb.Value_StringValue:
			text, cut := fence.Wrap(value.StringValue, output.MaxValueBytes)
			out[shown] = text
			*truncated = *truncated || cut
		default:
			*truncated = true
		}
	}

	return out
}

// safeCheckID passes a check ID the compiled-in catalog or serve names, fences one that only
// has the right shape, and replaces anything else.
func safeCheckID(id string, fence *output.Fence) string {
	switch {
	case !checkIDFormat.MatchString(id):
		return invalidCheckID
	case catalogview.KnownCheckID(id):
		return id
	default:
		fenced, _ := fence.Wrap(id, len(id))
		return fenced
	}
}

// fencedID says whether a check ID in out was fenced, so the caller needs the marker.
func fencedID(out *VerdictOutput) bool {
	ids := slices.Clone(out.Passed)
	for _, found := range out.Findings {
		ids = append(ids, found.CheckID)
	}

	for _, skipped := range out.Skipped {
		ids = append(ids, skipped.CheckID)
	}

	return slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, "<") })
}

func safeVersion(version string) string {
	if versionFormat.MatchString(version) {
		return version
	}

	return ""
}

func safeActions(actions []string) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		if slices.Contains(catalogview.RemediationActions, action) {
			out = append(out, action)
		} else {
			out = append(out, unknownAction)
		}
	}

	return out
}

// fitVerdict cuts the answer to MaxVerdictBytes: passed and skipped lists first, then
// observed values, then findings from the least severe end.
func fitVerdict(out *VerdictOutput) {
	size := func() int {
		raw, _ := json.Marshal(out)
		return len(raw)
	}

	if size() <= MaxVerdictBytes {
		return
	}

	out.Truncated = true
	out.Passed, out.Skipped = nil, nil

	for i := range out.Findings {
		if size() <= MaxVerdictBytes {
			return
		}

		out.Findings[i].ObservedValues = nil
	}

	for size() > MaxVerdictBytes && len(out.Findings) > 1 {
		out.Findings = out.Findings[:len(out.Findings)-1]
	}
}
