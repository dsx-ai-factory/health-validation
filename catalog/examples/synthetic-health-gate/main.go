// SPDX-FileCopyrightText: 2026 Tejesh Vaish
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

type assessment struct {
	AssessmentAvailable bool `json:"assessment_available"`
}

func (assessment) Category() health.Category { return health.Node }

func (assessment) Errors() []health.ErroredMeasurement { return nil }

func syntheticAssessment() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "synthetic_assessment",
		Name:              "Synthetic assessment",
		Description:       "Demonstrates an unavailable synthetic assessment through an UNKNOWN rule.",
		Category:          health.Node,
		Provenance:        health.Private,
		MaturityLevel:     health.Experimental,
		ConditionScope:    health.NodeScope,
		ExecutionClass:    health.Passive,
		SchedulingCadence: health.Continuous,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		RequiredSignals:   []string{"assessment_available"},
		Rules: []health.Rule{{
			Expression: "assessment_available == false",
			Severity:   health.Unknown,
			Message:    "Synthetic assessment unavailable.",
		}},
	}
}

type scenario struct {
	measurements []health.Measurement
	disabled     []string
}

func selectScenario(name string) (scenario, error) {
	fixture := scenario{measurements: []health.Measurement{
		cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 8},
		assessment{AssessmentAvailable: true},
	}}
	switch name {
	case "pass":
	case "critical":
		fixture.measurements[0] = cpu.CPUMeasurement{PossibleCPUCount: 8, OnlineCPUCount: 7}
	case "unknown":
		fixture.measurements[1] = assessment{AssessmentAvailable: false}
	case "missing":
		fixture.measurements = fixture.measurements[1:]
	case "disabled":
		fixture.disabled = []string{"cpu_count"}
	case "collection-error":
		fixture.measurements[0] = cpu.ErroredCPU(errors.New("synthetic collection failure"))
	case "empty":
		fixture.measurements = nil
	default:
		return scenario{}, fmt.Errorf("-scenario must be pass, critical, unknown, missing, disabled, collection-error or empty; got %q", name)
	}
	return fixture, nil
}

func newCatalog(disabled []string) (*catalog.Catalog, error) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("synthetic"),
		catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithDisabledChecks(disabled...),
	)
	for _, check := range []health.CheckDefinition{cpu.CPUCount(), syntheticAssessment()} {
		if err := cat.Register(check); err != nil {
			return nil, fmt.Errorf("register %s: %w", check.ID, err)
		}
	}
	return cat, nil
}

func evaluate(cat *catalog.Catalog, measurements []health.Measurement) (health.NodeVerdict, error) {
	verdict, err := cat.EvaluateAll(measurements...)
	if err != nil {
		return health.NodeVerdict{}, fmt.Errorf("evaluate: %w", err)
	}
	verdict.NodeID = "synthetic-node"
	return verdict, nil
}

type decision struct {
	reason  string
	proceed bool
}

func validateContract(verdict health.NodeVerdict) error {
	if verdict.SchemaVersion != "0.4" {
		return fmt.Errorf("unsupported verdict schema %q; require 0.4", verdict.SchemaVersion)
	}
	if verdict.CatalogVersion == "" {
		return errors.New("missing catalog version")
	}
	return validateSeverities(verdict)
}

func validateSeverities(verdict health.NodeVerdict) error {
	severities := []health.Severity{verdict.OverallSeverity}
	for _, result := range verdict.CheckResults {
		severities = append(severities, result.Severity)
	}
	for _, skip := range verdict.SkippedChecks {
		severities = append(severities, skip.Severity)
	}
	for _, severity := range verdict.SeverityByDomain {
		severities = append(severities, severity)
	}
	for _, component := range verdict.AffectedComponents {
		severities = append(severities, component.MaxSeverity)
	}
	for _, severity := range severities {
		if severity < health.Pass || severity > health.Unknown {
			return fmt.Errorf("invalid verdict severity %d", severity)
		}
	}
	return nil
}

func completeCoverage(verdict health.NodeVerdict) bool {
	required := map[string]health.Category{
		"cpu_count":            health.CPU,
		"synthetic_assessment": health.Node,
	}
	if len(verdict.SkippedChecks) != 0 || len(verdict.CheckResults) != len(required) {
		return false
	}
	seen := make(map[string]bool)
	for _, result := range verdict.CheckResults {
		category, expected := required[result.CheckID]
		if !expected || result.Category != category || result.Skipped || result.SkipReason != health.NotSkipped || seen[result.CheckID] {
			return false
		}
		seen[result.CheckID] = true
	}
	return true
}

func decide(verdict health.NodeVerdict) (decision, error) {
	if err := validateContract(verdict); err != nil {
		return decision{}, err
	}
	if !completeCoverage(verdict) {
		return decision{reason: "coverage"}, nil
	}
	worst := verdict.OverallSeverity
	for _, result := range verdict.CheckResults {
		worst = health.Worse(worst, result.Severity)
	}
	return decision{reason: worst.String(), proceed: worst == health.Pass}, nil
}

func consume(verdict health.NodeVerdict, output io.Writer, next func() error) (decision, error) {
	outcome, err := decide(verdict)
	if err != nil {
		return outcome, err
	}
	if err := json.NewEncoder(output).Encode(verdict); err != nil {
		return outcome, fmt.Errorf("write verdict: %w", err)
	}
	if outcome.proceed {
		if err := next(); err != nil {
			return outcome, fmt.Errorf("continue: %w", err)
		}
	}
	return outcome, nil
}

func evaluateAndConsume(cat *catalog.Catalog, measurements []health.Measurement, output io.Writer, next func() error) (decision, error) {
	verdict, err := evaluate(cat, measurements)
	if err != nil {
		return decision{}, err
	}
	return consume(verdict, output, next)
}

type continuation struct {
	output io.Writer
}

func (c continuation) next() error {
	_, err := fmt.Fprintln(c.output, "continued synthetic workload")
	return err
}

func parseScenario(args []string, diagnostics io.Writer) (scenario, error) {
	flags := flag.NewFlagSet("synthetic-health-gate", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	name := flags.String("scenario", "", "required synthetic scenario")
	if err := flags.Parse(args); err != nil {
		return scenario{}, err
	}
	if flags.NArg() != 0 {
		return scenario{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return selectScenario(*name)
}

func run(args []string, stdout, stderr io.Writer) int {
	code, err := runExample(args, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return code
}

func runExample(args []string, stdout, stderr io.Writer) (int, error) {
	fixture, err := parseScenario(args, stderr)
	if err != nil {
		return 2, err
	}
	cat, err := newCatalog(fixture.disabled)
	if err != nil {
		return 2, err
	}
	next := continuation{output: stderr}
	decision, err := evaluateAndConsume(cat, fixture.measurements, stdout, next.next)
	if err != nil {
		return 2, err
	}
	if !decision.proceed {
		if _, err := fmt.Fprintf(stderr, "withheld: %s\n", decision.reason); err != nil {
			return 2, err
		}
		return 1, nil
	}
	return 0, nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
