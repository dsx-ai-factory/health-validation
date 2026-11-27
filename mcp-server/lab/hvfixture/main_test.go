// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

// What each scenario must produce: the claim in scenarios.go, checked against hv's own
// verdict over the wire, so a catalog change that alters a scenario fails here first.
var expected = map[string]struct {
	severity    healthv1.Severity
	state       healthv1.OperationalState
	failing     []string
	mustRun     []string
	livenessOK  bool
	notRunCount int
}{
	"healthy-gb200": {
		severity: healthv1.Severity_PASS, livenessOK: true,
		mustRun: []string{"gpu_count", "gpu_power_limit", "nvlink_active", "nvlink_bandwidth_count_speed", "gpu_xid_79"},
	},
	"nvlink-down": {
		severity: healthv1.Severity_CRITICAL, state: healthv1.OperationalState_drain, livenessOK: true,
		failing: []string{"nvlink_active"},
	},
	"xid79": {
		severity: healthv1.Severity_CRITICAL, state: healthv1.OperationalState_drain, livenessOK: true,
		failing: []string{"gpu_xid_79"},
	},
	"busy-source": {
		severity: healthv1.Severity_UNKNOWN, state: healthv1.OperationalState_drain,
		failing: []string{"collection_source_busy"},
	},
	"discovery-failure": {
		severity: healthv1.Severity_CRITICAL, state: healthv1.OperationalState_drain, livenessOK: true,
		failing: []string{"architecture_discovery"},
	},
	"unknown-architecture": {
		severity: healthv1.Severity_PASS, livenessOK: true, mustRun: []string{"cpu_count"},
	},
	"overridden-threshold": {
		severity: healthv1.Severity_CRITICAL, state: healthv1.OperationalState_drain, livenessOK: true,
		failing: []string{"gpu_average_power_draw"},
	},
}

func TestEveryScenarioIsVerified(t *testing.T) {
	for _, name := range scenarioNames() {
		if _, ok := expected[name]; !ok {
			t.Errorf("scenario %q has no expected verdict", name)
		}
	}

	if len(expected) != len(scenarios) {
		t.Errorf("expected lists %d scenarios, there are %d", len(expected), len(scenarios))
	}
}

func TestScenariosYieldTheVerdictTheyClaim(t *testing.T) {
	for _, name := range scenarioNames() {
		t.Run(name, func(t *testing.T) {
			want := expected[name]
			address, _ := start(t, settings{address: "127.0.0.1:0", scenario: name})

			verdict := post(t, address, `{}`)

			if got := verdict.GetOverallSeverity(); got != want.severity {
				t.Fatalf("overall severity %s, want %s", got, want.severity)
			}

			if want.state != healthv1.OperationalState_OPERATIONAL_STATE_UNSPECIFIED {
				if got := verdict.GetRecommendedAction().GetOperationalState(); got != want.state {
					t.Errorf("operational state %s, want %s", got, want.state)
				}
			}

			var failing, ran []string

			for _, result := range verdict.GetCheckResults() {
				ran = append(ran, result.GetCheckId())

				if result.GetSeverity() != healthv1.Severity_PASS {
					failing = append(failing, result.GetCheckId())
				}
			}

			slices.Sort(failing)
			failing = slices.Compact(failing)

			if !slices.Equal(failing, want.failing) {
				t.Errorf("failing checks %v, want %v", failing, want.failing)
			}

			for _, id := range want.mustRun {
				if !slices.Contains(ran, id) {
					t.Errorf("%s did not run", id)
				}
			}

			if verdict.GetNodeId() != defaultNodeID {
				t.Errorf("node_id %q, want %q", verdict.GetNodeId(), defaultNodeID)
			}

			if verdict.GetSchemaVersion() != health.NodeVerdictSchemaVersion {
				t.Errorf("schema %q, want %q", verdict.GetSchemaVersion(), health.NodeVerdictSchemaVersion)
			}

			checkLiveness(t, address, want.livenessOK)
		})
	}
}

// The GB200 scenario must reach GPU, NVLink and Xid checks; the unknown one must reach none,
// which is what makes its PASS low coverage.
func TestCoverageOfThePassScenarios(t *testing.T) {
	count := func(verdict *healthv1.NodeVerdict) int {
		total := 0

		for _, result := range verdict.GetCheckResults() {
			switch result.GetCategory() {
			case healthv1.Category_gpu, healthv1.Category_nvlink, healthv1.Category_xid:
				total++
			default:
			}
		}

		return total
	}

	gb200, _ := start(t, settings{address: "127.0.0.1:0", scenario: "healthy-gb200"})
	if got := count(post(t, gb200, `{}`)); got < 10 {
		t.Errorf("healthy-gb200 ran %d GPU, NVLink and Xid checks, want many", got)
	}

	unknown, _ := start(t, settings{address: "127.0.0.1:0", scenario: "unknown-architecture"})

	verdict := post(t, unknown, `{}`)
	if got := count(verdict); got != 0 {
		t.Errorf("unknown-architecture ran %d GPU, NVLink and Xid checks, want 0", got)
	}

	architecture := 0

	for _, skipped := range verdict.GetSkippedChecks() {
		if skipped.GetSkipReason() == healthv1.SkipReason_SIGNAL_UNAVAILABLE_ARCHITECTURE {
			architecture++
		}
	}

	if architecture == 0 {
		t.Error("unknown-architecture skipped nothing for the architecture")
	}
}

// Without the override, the same measurements pass: the override is what fails the node.
func TestTheOverrideIsWhatFails(t *testing.T) {
	verdict, err := judge("GB200", nil, nil, gb200(nil)...)
	if err != nil {
		t.Fatal(err)
	}

	if verdict.OverallSeverity != health.Pass {
		t.Fatalf("without the override: %s, want PASS", verdict.OverallSeverity)
	}
}

func TestCadenceIsApplied(t *testing.T) {
	address, _ := start(t, settings{address: "127.0.0.1:0", scenario: "healthy-gb200"})

	all := post(t, address, `{}`)
	continuous := post(t, address, `{"cadence":"CONTINUOUS"}`)

	mismatched := 0

	for _, skipped := range continuous.GetSkippedChecks() {
		if skipped.GetSkipReason() == healthv1.SkipReason_CADENCE_MISMATCH {
			mismatched++
		}
	}

	if mismatched == 0 || len(continuous.GetCheckResults()) >= len(all.GetCheckResults()) {
		t.Errorf("CONTINUOUS ran %d checks with %d cadence skips; ALL ran %d",
			len(continuous.GetCheckResults()), mismatched, len(all.GetCheckResults()))
	}
}

func TestHoldEndsWhenTheCallerGoesAway(t *testing.T) {
	address, events := start(t, settings{address: "127.0.0.1:0", scenario: "healthy-gb200", hold: time.Minute})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/verdict", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	answered := make(chan error, 1)

	go func() {
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}

		answered <- err
	}()

	// Cancel only once the fixture holds the call, so a slow start cannot fail the test.
	waitForEvent(t, events, "evaluate_received")
	cancel()

	if err := <-answered; err == nil {
		t.Fatal("the held call answered")
	}

	waitForEvent(t, events, "evaluate_cancelled")
}

// waitForEvent waits for the fixture to log an event, and fails after a generous watchdog.
func waitForEvent(t *testing.T, events *lockedBuffer, name string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(events.String(), `"msg":"`+name+`"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no %s logged: %s", name, events.String())
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestOnlyNumericLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", "localhost:8080", "192.168.1.2:8080", "[::]:8080", "127.0.0.1"} {
		if err := validate(settings{address: address, scenario: "xid79"}); err == nil {
			t.Errorf("%s was accepted", address)
		}
	}

	for _, address := range []string{"127.0.0.1:0", "[::1]:8080"} {
		if err := validate(settings{address: address, scenario: "xid79"}); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}

	if err := validate(settings{address: "127.0.0.1:0", scenario: "nope"}); err == nil {
		t.Error("an unknown scenario was accepted")
	}
}

type lockedBuffer struct {
	buffer bytes.Buffer
	mutex  sync.Mutex
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.String()
}

func start(t *testing.T, config settings) (string, *lockedBuffer) {
	t.Helper()

	events := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)

	go func() {
		err := serve(ctx, config, writer, slog.New(slog.NewJSONHandler(events, nil)))
		// Closing the writer frees the readiness read below if serve fails to start, and
		// the drain after shutdown.
		_ = writer.CloseWithError(err)
		done <- err
	}()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})

	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	go func() { _, _ = io.Copy(io.Discard, reader) }()

	return strings.TrimSpace(strings.TrimPrefix(line, "listening on ")), events
}

func post(t *testing.T, address, body string) *healthv1.NodeVerdict {
	t.Helper()

	response, err := http.Post("http://"+address+"/verdict", "application/json", strings.NewReader(body)) //nolint:noctx // A test.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, raw)
	}

	verdict := &healthv1.NodeVerdict{}
	if err := (protojson.UnmarshalOptions{}).Unmarshal(raw, verdict); err != nil {
		t.Fatalf("decode: %v: %s", err, raw)
	}

	return verdict
}

func checkLiveness(t *testing.T, address string, wantOK bool) {
	t.Helper()

	response, err := http.Get("http://" + address + "/healthz") //nolint:noctx // A test.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	var body struct {
		Status     string `json:"status"`
		StuckCalls []struct {
			Source string `json:"source"`
		} `json:"stuck_calls"`
	}

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if wantOK {
		if response.StatusCode != http.StatusOK || body.Status != "ok" {
			t.Errorf("/healthz %d %q, want 200 ok", response.StatusCode, body.Status)
		}

		return
	}

	if response.StatusCode != http.StatusServiceUnavailable || body.Status != "unhealthy" ||
		len(body.StuckCalls) != 1 || body.StuckCalls[0].Source != "nvml" {
		t.Errorf("/healthz %d %+v, want 503 unhealthy with the nvml call", response.StatusCode, body)
	}
}
