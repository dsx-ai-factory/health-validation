// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build evals

// Package evals checks the answers in docs/evals/evals.xml. For each question it drives
// the built server over stdio, against lab/hvfixture where the question names a scenario,
// and works the answer out with the same tool calls a model would make. A wrong answer in
// the file, or a tool that no longer gives what the question needs, fails the test.
// `make evals` runs it.
package evals

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	catalogOnly = "catalog"
	callTimeout = time.Minute
)

var binary, fixture, evalsFile string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "hv-mcp-evals-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	code := func() int {
		defer func() { _ = os.RemoveAll(directory) }()

		if err := buildAll(directory); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}

		return m.Run()
	}()

	os.Exit(code)
}

func buildAll(directory string) error {
	root, err := filepath.Abs("../..")
	if err != nil {
		return err
	}

	evalsFile = filepath.Join(root, "docs", "evals", "evals.xml")
	binary = filepath.Join(directory, "health-validation-mcp")
	fixture = filepath.Join(directory, "hvfixture")

	if err := goBuild(root, binary, "./cmd/health-validation-mcp"); err != nil {
		return err
	}

	return goBuild(filepath.Join(root, "lab", "hvfixture"), fixture, ".")
}

func goBuild(directory, output, target string) error {
	command := exec.Command("go", "build", "-trimpath", "-o", output, target)
	command.Dir = directory
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		return fmt.Errorf("build %s in %s: %w", target, directory, err)
	}

	return nil
}

// qaPair is one question in the file, with the scenario its comment names.
type qaPair struct {
	Scenario string
	Question string `xml:"question"`
	Answer   string `xml:"answer"`
}

// readEvals reads the qa_pairs in order. The comment just before each names its scenario.
func readEvals(t *testing.T) []qaPair {
	t.Helper()

	file, err := os.Open(evalsFile)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	var (
		pairs    []qaPair
		scenario string
	)

	decoder := xml.NewDecoder(file)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return pairs
		}

		require.NoError(t, err)

		switch element := token.(type) {
		case xml.Comment:
			if name, ok := strings.CutPrefix(strings.TrimSpace(string(element)), "scenario:"); ok {
				scenario = strings.TrimSpace(name)
			}
		case xml.StartElement:
			if element.Name.Local != "qa_pair" {
				continue
			}

			var pair qaPair

			require.NoError(t, decoder.DecodeElement(&pair, &element))
			require.NotEmpty(t, scenario, "qa_pair %d has no scenario comment", len(pairs)+1)

			pair.Scenario, scenario = scenario, ""
			pair.Question, pair.Answer = strings.TrimSpace(pair.Question), strings.TrimSpace(pair.Answer)
			pairs = append(pairs, pair)
		}
	}
}

// solver works out one question's answer through the tools.
type solver struct {
	solve    func(t *testing.T, s *session) string
	scenario string
	// asks is a phrase from the question the solver answers, so an edited question
	// fails until its solver is checked again.
	asks string
}

// The solvers, in the order of the qa_pairs in docs/evals/evals.xml.
var solvers = []solver{
	{scenario: catalogOnly, solve: notContinuous, asks: "never run in that window"},
	{scenario: catalogOnly, solve: nvlinkNotOnH100, asks: "NVLink checks in the catalog will not run on them"},
	{scenario: catalogOnly, solve: gb300PowerHeadroom, asks: "by how many watts does the default maximum average power draw"},
	{scenario: catalogOnly, solve: onlyStepOfXid78, asks: "gpu_xid_78 recommends one remediation step"},
	{scenario: "unknown-architecture", solve: adviceForAPass, asks: "Can the scheduler treat it as a healthy GPU node?"},
	{scenario: "discovery-failure", solve: adviceWithoutDiscovery, asks: "No GPU, NVLink or Xid check ran on this node."},
	{scenario: "xid79", solve: xidOccurrences, asks: "how many events of that Xid code on one GPU"},
	{scenario: "overridden-threshold", solve: catalogPowerMax, asks: "does the catalog itself set"},
	{scenario: "busy-source", solve: stuckSource, asks: "has a call that has not returned"},
	{scenario: "healthy-gb200", solve: xidSkippedOnGB200, asks: "Some Xid checks were skipped"},
}

func TestEvaluationAnswers(t *testing.T) {
	pairs := readEvals(t)
	require.Len(t, pairs, len(solvers), "each qa_pair needs a solver, in the same order")

	for i, pair := range pairs {
		t.Run(fmt.Sprintf("q%02d-%s", i+1, pair.Scenario), func(t *testing.T) {
			t.Parallel()

			require.Equal(t, solvers[i].scenario, pair.Scenario, "question: %s", pair.Question)
			require.Contains(t, pair.Question, solvers[i].asks, "the solver answers a different question")

			s := open(t, pair.Scenario)
			got := solvers[i].solve(t, s)
			require.Equal(t, pair.Answer, got, "question: %s\ntool calls: %d", pair.Question, s.calls)
			require.GreaterOrEqual(t, s.calls, 2, "a question should need several tool calls")
		})
	}
}

// session is the server under test, with a count of the tool calls made through it.
type session struct {
	client *mcp.ClientSession
	calls  int
}

func open(t *testing.T, scenario string) *session {
	t.Helper()

	environment := []string{"HV_MCP_SERVE_PORT=" + closedPort(t)}
	if scenario != catalogOnly {
		environment = []string{"HV_MCP_SERVE_PORT=" + startFixture(t, scenario), "HV_MCP_ALLOW_EVALUATE=true"}
	}

	command := exec.Command(binary) //nolint:gosec // The test's own build.
	command.Env = environment
	command.Stderr = io.Discard

	client := mcp.NewClient(&mcp.Implementation{Name: "evals", Version: "test"}, nil)

	handshake, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	connected, err := client.Connect(handshake, &mcp.CommandTransport{Command: command}, nil)
	require.NoError(t, err, "the MCP handshake")
	t.Cleanup(func() { _ = connected.Close() })

	return &session{client: connected}
}

func startFixture(t *testing.T, scenario string) string {
	t.Helper()

	command := exec.Command(fixture, "--scenario", scenario) //nolint:gosec // The test's own build.
	command.Stderr = io.Discard

	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })

	type ready struct {
		err  error
		line string
	}

	lines := make(chan ready, 1)

	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		lines <- ready{line: line, err: err}
	}()

	var line string

	select {
	case got := <-lines:
		require.NoError(t, got.err)

		line = got.line
	case <-time.After(30 * time.Second):
		// The cleanup kills and reaps the fixture.
		require.FailNow(t, "hvfixture did not report its address in time")
	}

	_, port, err := net.SplitHostPort(strings.TrimSpace(strings.TrimPrefix(line, "listening on ")))
	require.NoError(t, err)

	return port
}

func closedPort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	return port
}

// call runs one tool and decodes its structured result into out.
func (s *session) call(t *testing.T, tool string, arguments map[string]any, out any) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()

	if arguments == nil {
		arguments = map[string]any{}
	}

	s.calls++

	result, err := s.client.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	require.NoError(t, err, tool)
	require.False(t, result.IsError, "%s %v: %v", tool, arguments, result.Content)

	encoded, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, out), tool)
}

type row struct {
	ID            string   `json:"id"`
	Cadence       string   `json:"cadence"`
	Architectures []string `json:"architectures"`
}

type listing struct {
	Checks []row `json:"checks"`
	Pages  int   `json:"pages"`
}

// listAll pages through hv_list_checks with a filter.
func (s *session) listAll(t *testing.T, filter map[string]any) []row {
	t.Helper()

	var rows []row

	for page := 1; ; page++ {
		arguments := map[string]any{"page": page}
		for key, value := range filter {
			arguments[key] = value
		}

		var out listing

		s.call(t, "hv_list_checks", arguments, &out)
		rows = append(rows, out.Checks...)

		if page >= out.Pages {
			return rows
		}
	}
}

type threshold struct {
	Default         any            `json:"default"`
	PerArchitecture map[string]any `json:"per_architecture"`
	Name            string         `json:"name"`
}

type description struct {
	ID                 string      `json:"id"`
	RemediationActions []string    `json:"remediation_actions"`
	Thresholds         []threshold `json:"thresholds"`
	Rules              []struct {
		RemediationActions []string `json:"remediation_actions"`
	} `json:"rules"`
}

func (s *session) describe(t *testing.T, id string) description {
	t.Helper()

	var out description

	s.call(t, "hv_describe_check", map[string]any{"check_id": id}, &out)

	return out
}

// thresholdValue is a threshold's value for an architecture, or its default with "".
func thresholdValue(t *testing.T, check description, name, architecture string) float64 {
	t.Helper()

	for _, spec := range check.Thresholds {
		if spec.Name != name {
			continue
		}

		value := spec.Default
		if specific, ok := spec.PerArchitecture[architecture]; ok && architecture != "" {
			value = specific
		}

		number, ok := value.(float64)
		require.True(t, ok, "%s.%s on %q is %v", check.ID, name, architecture, value)

		return number
	}

	require.FailNow(t, "no threshold "+name+" on "+check.ID)

	return 0
}

type finding struct {
	CheckID  string   `json:"check_id"`
	Entities []string `json:"entities"`
	Messages []string `json:"messages"`
}

type skip struct {
	CheckID string `json:"check_id"`
	Reason  string `json:"reason"`
}

type verdict struct {
	Coverage struct {
		GPUChecksRun    *int `json:"gpu_checks_run"`
		NVLinkChecksRun *int `json:"nvlink_checks_run"`
		XidChecksRun    *int `json:"xid_checks_run"`
		LowCoverage     bool `json:"low_coverage"`
	} `json:"coverage"`
	Decision struct {
		OverallSeverity  string `json:"overall_severity"`
		SchedulingAdvice string `json:"scheduling_advice"`
	} `json:"decision"`
	FenceMarker string    `json:"fence_marker"`
	Findings    []finding `json:"findings"`
	Skipped     []skip    `json:"skipped"`
}

// evaluate checks that serve answers, as the tool descriptions advise, then asks for a verdict.
func (s *session) evaluate(t *testing.T, detail string) verdict {
	t.Helper()

	var status struct {
		Reachable       bool `json:"reachable"`
		EvaluateEnabled bool `json:"evaluate_enabled"`
	}

	s.call(t, "hv_service_status", nil, &status)
	require.True(t, status.Reachable && status.EvaluateEnabled, "%+v", status)

	var out verdict

	s.call(t, "hv_get_verdict", map[string]any{"detail": detail}, &out)

	return out
}

func unfence(text, marker string) string {
	if marker == "" {
		return text
	}

	return strings.NewReplacer("<"+marker+">", "", "</"+marker+">", "").Replace(text)
}

func sortedList(ids []string) string { return strings.Join(unique(ids), ", ") }

func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func notContinuous(t *testing.T, s *session) string {
	var ids []string

	for _, check := range s.listAll(t, nil) {
		if check.Cadence != "CONTINUOUS" {
			ids = append(ids, check.ID)
		}
	}

	return sortedList(ids)
}

func nvlinkNotOnH100(t *testing.T, s *session) string {
	onH100 := map[string]bool{}
	for _, check := range s.listAll(t, map[string]any{"category": "nvlink", "architecture": "H100"}) {
		onH100[check.ID] = true
	}

	var ids []string

	for _, check := range s.listAll(t, map[string]any{"category": "nvlink"}) {
		if !onH100[check.ID] {
			ids = append(ids, check.ID)
		}
	}

	return sortedList(ids)
}

func gb300PowerHeadroom(t *testing.T, s *session) string {
	maximum := thresholdValue(t, s.describe(t, "gpu_average_power_draw"), "max_power_w", "GB300")
	limit := thresholdValue(t, s.describe(t, "gpu_power_limit"), "expected_power_limit_w", "GB300")

	return number(maximum - limit)
}

func onlyStepOfXid78(t *testing.T, s *session) string {
	check := s.describe(t, "gpu_xid_78")

	steps := slices.Clone(check.RemediationActions)
	for _, rule := range check.Rules {
		steps = append(steps, rule.RemediationActions...)
	}

	var only []string

	for _, step := range unique(steps) {
		var out struct {
			RecommendedBy []string `json:"recommended_by"`
			Total         int      `json:"total"`
		}

		s.call(t, "hv_explain_remediation", map[string]any{"action": step}, &out)

		if out.Total == 1 && slices.Equal(out.RecommendedBy, []string{"gpu_xid_78"}) {
			only = append(only, step)
		}
	}

	require.Len(t, only, 1, "the question says exactly one step")

	return only[0]
}

func unique(values []string) []string {
	slices.Sort(values)

	return slices.Compact(values)
}

// The question's premise: a PASS. The answer: not healthy, low coverage.
func adviceForAPass(t *testing.T, s *session) string {
	out := s.evaluate(t, "summary")
	require.Equal(t, "PASS", out.Decision.OverallSeverity)
	require.True(t, out.Coverage.LowCoverage)
	requireNoCoverage(t, out)

	return out.Decision.SchedulingAdvice
}

func adviceWithoutDiscovery(t *testing.T, s *session) string {
	out := s.evaluate(t, "summary")
	requireNoCoverage(t, out)

	return out.Decision.SchedulingAdvice
}

func xidOccurrences(t *testing.T, s *session) string {
	out := s.evaluate(t, "failing")
	require.Equal(t, "DRAIN", out.Decision.SchedulingAdvice)
	require.Len(t, out.Findings, 1)
	require.True(t, strings.HasPrefix(out.Findings[0].CheckID, "gpu_xid_"), out.Findings[0].CheckID)

	return number(thresholdValue(t, s.describe(t, out.Findings[0].CheckID), "occurrences", ""))
}

func catalogPowerMax(t *testing.T, s *session) string {
	out := s.evaluate(t, "failing")
	require.Equal(t, "DRAIN", out.Decision.SchedulingAdvice)
	require.NotEmpty(t, out.Findings)

	var ids []string
	for _, failed := range out.Findings {
		ids = append(ids, failed.CheckID)
	}

	ids = unique(ids)
	require.Len(t, ids, 1, "one check fails, on every GPU")

	return number(thresholdValue(t, s.describe(t, ids[0]), "max_power_w", "GB200"))
}

func stuckSource(t *testing.T, s *session) string {
	var out verdict

	s.call(t, "hv_get_verdict", map[string]any{"detail": "summary"}, &out)
	require.Equal(t, "UNKNOWN", out.Decision.OverallSeverity)
	require.Equal(t, "RETRY_COLLECTION", out.Decision.SchedulingAdvice)

	var status struct {
		FenceMarker string `json:"fence_marker"`
		StuckCalls  []struct {
			Source string `json:"source"`
		} `json:"stuck_calls"`
	}

	s.call(t, "hv_service_status", nil, &status)
	require.Len(t, status.StuckCalls, 1)

	return unfence(status.StuckCalls[0].Source, status.FenceMarker)
}

func xidSkippedOnGB200(t *testing.T, s *session) string {
	out := s.evaluate(t, "full")
	require.Equal(t, "READY", out.Decision.SchedulingAdvice)

	var ids []string

	for _, skipped := range out.Skipped {
		if skipped.Reason == "SIGNAL_UNAVAILABLE_ARCHITECTURE" && strings.HasPrefix(skipped.CheckID, "gpu_xid_") {
			ids = append(ids, skipped.CheckID)
		}
	}

	return sortedList(ids)
}

// requireNoCoverage checks that the verdict reports each coverage counter, and that each
// is zero: an absent counter must not read as no checks run.
func requireNoCoverage(t *testing.T, out verdict) {
	t.Helper()

	for name, counter := range map[string]*int{
		"gpu_checks_run": out.Coverage.GPUChecksRun, "nvlink_checks_run": out.Coverage.NVLinkChecksRun,
		"xid_checks_run": out.Coverage.XidChecksRun,
	} {
		require.NotNil(t, counter, "coverage.%s is missing", name)
		require.Zero(t, *counter, "coverage.%s", name)
	}
}
