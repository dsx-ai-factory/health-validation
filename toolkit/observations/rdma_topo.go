// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	rdmatoposignals "github.com/dsx-ai-factory/health-validation/collect/signals/rdmatopo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var rdmaTopoFields = map[string]string{
	rdmatoposignals.ACSFailures.ID():        pcie.SignalACSFailures,
	rdmatoposignals.IOMMUGroupFailures.ID(): pcie.SignalIOMMUGroupFailures,
	rdmatoposignals.Evidence.ID():           pcie.SignalEvidence,
}

// canonicalRDMATopo keeps confirmed findings separate from unassessed scope.
// Putting errors on the findings measurement would prevent its rule evaluating.
func canonicalRDMATopo(obs []*observation.Observation) []health.Measurement {
	values := make(map[string]*observation.Observation)
	failed := make(map[string]bool)
	var failures []error
	addError := func(err error) {
		if !slices.ContainsFunc(failures, func(old error) bool {
			return old.Error() == err.Error() && errors.Is(old, health.ErrCollectionTimeout) == errors.Is(err, health.ErrCollectionTimeout)
		}) {
			failures = append(failures, err)
		}
	}
	var source, node string
	var identified bool
	for _, o := range obs {
		id := o.GetSignalId()
		if o.GetValue() == nil {
			if failed[id] {
				addError(fmt.Errorf("signal %q failure was observed more than once", id))
				continue
			}
			failed[id] = true
			if o.GetTarget() != nil {
				addError(fmt.Errorf("signal %q collection failure must be source-scoped", id))
			}
			switch {
			case o.GetCollectionError() != nil:
				addError(collectionFailure(o.GetCollectionError()))
			case o.GetNoValue() != nil:
				addError(noValueFailure(o.GetSource(), o.GetNoValue()))
			default:
				addError(fmt.Errorf("signal %q has no outcome", id))
			}
			continue
		}
		if _, exists := values[id]; exists {
			values[id] = nil
			addError(fmt.Errorf("signal %q was observed more than once", id))
			continue
		}
		values[id] = o
		if o.GetTarget().GetNode() == nil {
			values[id] = nil
			addError(fmt.Errorf("signal %q has no node target", id))
			continue
		}
		if !identified {
			source, node, identified = o.GetSource(), o.GetTarget().GetNode().GetId(), true
		} else if source != o.GetSource() || node != o.GetTarget().GetNode().GetId() {
			values[id] = nil
			addError(fmt.Errorf("signal %q does not belong to the same node and source", id))
		}
	}
	var m pcie.RDMATopoMeasurement
	for _, signal := range rdmatoposignals.All() {
		o := values[signal.ID()]
		if o == nil {
			if !failed[signal.ID()] {
				addError(fmt.Errorf("rdma_topo: missing %s", signal.ID()))
			}
			continue
		}
		if _, ok := o.GetValue().GetKind().(*observation.Value_StringValue); !ok {
			addError(fmt.Errorf("signal %q does not have a JSON string value", signal.ID()))
			continue
		}
		if o.GetUnit() != "" {
			addError(fmt.Errorf("signal %q has unexpected unit %q", signal.ID(), o.GetUnit()))
			continue
		}
		data := []byte(o.GetValue().GetStringValue())
		var err error
		switch signal.ID() {
		case rdmatoposignals.ACSFailures.ID():
			m.ACSFailures, err = decodeRDMATopoFindings(data)
		case rdmatoposignals.IOMMUGroupFailures.ID():
			m.IOMMUGroupFailures, err = decodeRDMATopoFindings(data)
		case rdmatoposignals.Evidence.ID():
			evidence := pcie.Evidence{ExitCode: -1}
			if err = json.Unmarshal(data, &evidence); err == nil {
				m.Evidence = evidence
			}
		}
		if err != nil {
			addError(fmt.Errorf("signal %q: %w", signal.ID(), err))
		}
	}
	hasFindings := len(m.ACSFailures)+len(m.IOMMUGroupFailures) > 0
	if values[rdmatoposignals.Evidence.ID()] != nil && (m.Evidence.Mode != "auto" || m.Evidence.ToolVersion == "" ||
		len(m.Evidence.Invocation) != 2 || m.Evidence.Invocation[1] != "check" || m.Evidence.Invocation[0] == "") {
		addError(errors.New("rdma_topo invocation evidence is incomplete or invalid"))
	}
	if len(failures) == 0 && (m.Evidence.Host == "" || m.Evidence.Executable == "" ||
		(m.Evidence.ExitCode != 0 && m.Evidence.ExitCode != 100)) {
		addError(errors.New("completed rdma_topo inspection lacks host, executable, or exit status"))
	}
	if len(failures) == 0 && ((m.Evidence.ExitCode == 0 && hasFindings) || (m.Evidence.ExitCode == 100 && !hasFindings)) {
		addError(errors.New("rdma_topo exit status contradicts its findings"))
	}
	var out []health.Measurement
	if len(failures) == 0 || hasFindings {
		out = append(out, m)
	}
	if len(failures) != 0 {
		failure := pcie.RDMATopoMeasurement{Evidence: m.Evidence}
		for _, err := range failures {
			failure.AddError(err, pcie.MeasurementSignals()...)
		}
		out = append(out, failure)
	}
	return out
}

func decodeRDMATopoFindings(data []byte) ([]pcie.Finding, error) {
	var records []json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	if records == nil {
		return nil, errors.New("must contain a JSON list")
	}
	findings := make([]pcie.Finding, 0, len(records))
	var failures []error
	for i, record := range records {
		var finding pcie.Finding
		if err := json.Unmarshal(record, &finding); err != nil {
			failures = append(failures, fmt.Errorf("finding %d: %w", i, err))
			continue
		}
		if finding.Diagnostic == "" {
			failures = append(failures, fmt.Errorf("finding %d has no diagnostics", i))
			continue
		}
		findings = append(findings, finding)
	}
	return findings, errors.Join(failures...)
}

func rdmaTopoMeasurement(m health.Measurement) (pcie.RDMATopoMeasurement, bool) {
	switch value := m.(type) {
	case pcie.RDMATopoMeasurement:
		return value, true
	case *pcie.RDMATopoMeasurement:
		if value != nil {
			return *value, true
		}
	}
	return pcie.RDMATopoMeasurement{}, false
}

// exportRDMATopo writes shared invocation evidence once, with node-targeted
// findings and source-scoped failures, preserving both through a round trip.
func exportRDMATopo(source, nodeID string, at *timestamppb.Timestamp, measurements []pcie.RDMATopoMeasurement) ([]*observation.Observation, error) {
	var evidence *pcie.Evidence
	var findings *pcie.RDMATopoMeasurement
	failures := make(map[string][]error)
	for _, m := range measurements {
		errs := m.Errors()
		if len(errs) > 0 && len(m.ACSFailures)+len(m.IOMMUGroupFailures) > 0 {
			return nil, errors.New("rdma_topo findings and collection failures must use separate measurements")
		}
		if !reflect.DeepEqual(m.Evidence, pcie.Evidence{}) {
			if evidence != nil && !reflect.DeepEqual(*evidence, m.Evidence) {
				return nil, errors.New("rdma_topo measurements describe different invocations")
			}
			evidence = &m.Evidence
		}
		if len(errs) == 0 {
			if findings != nil {
				return nil, errors.New("rdma_topo findings were supplied more than once")
			}
			findings = &m
		}
		for _, failure := range errs {
			for _, signal := range failure.FailedSignals() {
				failures[signal] = append(failures[signal], failure.Err())
			}
		}
	}
	var out []*observation.Observation
	for _, signal := range rdmatoposignals.All() {
		field := rdmaTopoFields[signal.ID()]
		var value any
		switch field {
		case pcie.SignalEvidence:
			if evidence != nil {
				value = evidence
			}
		case pcie.SignalACSFailures:
			if findings != nil {
				value = findings.ACSFailures
			}
		case pcie.SignalIOMMUGroupFailures:
			if findings != nil {
				value = findings.IOMMUGroupFailures
			}
		}
		if value != nil {
			encoded, err := fieldValue(reflect.ValueOf(value))
			if err != nil {
				return nil, err
			}
			out = append(out, &observation.Observation{
				SignalId: signal.ID(), Unit: signal.Unit(), Source: source,
				ObservedAt: at, Target: observation.NodeTarget(nodeID),
				Outcome: &observation.Observation_Value{Value: encoded},
			})
		}
		if errs := failures[field]; len(errs) > 0 {
			out = append(out, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(),
				source, exportOperation, nil, at, collectionError(source, errs)))
		}
	}
	return out, nil
}
