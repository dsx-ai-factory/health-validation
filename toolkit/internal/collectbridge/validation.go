// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectbridge

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"google.golang.org/protobuf/proto"
)

// Target kinds a canonical signal's observations may describe.
const (
	targetNode           = "node"
	targetGPU            = "GPU"
	targetNVLinkEndpoint = "NVLink endpoint"
	targetGPUPair        = "GPU pair"
	targetGPUProcess     = "GPU process"
	targetJournalEntry   = "journal entry"
)

// canonicalTargets lists the target kind each canonical signal accepts.
// Migrating domains append entries; a later entry overrides an earlier one
// for the same signal, so a per-link or GPU-pair signal in gpusignals is
// declared after the whole-GPU entry. observations.targetID attributes
// endpoint and pair targets to their source GPU, so a new kind needs a
// matching mapping there.
//
// Node-scoped producers emit observation.NodeTarget(""), meaning the node
// that produced the batch: like collect's other targets, it is host-relative,
// and collect carries no node identity. Health Validation evaluates one node
// per run, so the ID is not part of a measurement's identity. Output that must
// name the node stamps the run's node ID, as observations.FromMeasurements does.
var canonicalTargets = []canonicalTarget{
	{kind: targetGPU, signals: gpusignals.All()},
	{kind: targetNode, signals: cpusignals.All()},
	{kind: targetNode, signals: collect.Signals{gpuinventory.GPUCount}},
	{kind: targetNVLinkEndpoint, signals: gpusignals.EndpointSignals()},
	{kind: targetGPUProcess, signals: gpusignals.ProcessEntitySignals()},
	{kind: targetJournalEntry, signals: xidsignals.EventSignals()},
	{kind: targetNode, signals: collect.Signals{xidsignals.ScanWindow}},
}

type canonicalTarget struct {
	kind    string
	signals collect.Signals
}

// canonicalRule is HV's acceptance rule for one canonical signal: its
// definition, which fixes the unit, and its target kind.
type canonicalRule struct {
	signal collect.Signal
	target string
}

var canonical = canonicalIndex(canonicalTargets)

func canonicalIndex(entries []canonicalTarget) map[string]canonicalRule {
	out := make(map[string]canonicalRule)
	for _, entry := range entries {
		for _, s := range entry.signals {
			out[s.ID()] = canonicalRule{signal: s, target: entry.kind}
		}
	}
	return out
}

// targetKind names the kind of a typed target, or "" for none.
func targetKind(target *observation.ObservationTarget) string {
	switch target.GetKind().(type) {
	case *observation.ObservationTarget_Node:
		return targetNode
	case *observation.ObservationTarget_GPU:
		return targetGPU
	case *observation.ObservationTarget_NVLinkEndpoint:
		return targetNVLinkEndpoint
	case *observation.ObservationTarget_GPUPair:
		return targetGPUPair
	case *observation.ObservationTarget_GPUProcess:
		return targetGPUProcess
	case *observation.ObservationTarget_JournalEntry:
		return targetJournalEntry
	default:
		return ""
	}
}

// sameCollectUnit treats a nil unit and an empty unit as different.
func sameCollectUnit(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// validateCollectDefinition rejects empty IDs and canonical signals declared
// with a different unit.
func validateCollectDefinition(s collect.Signal) error {
	if s == nil || strings.TrimSpace(s.ID()) == "" {
		return fmt.Errorf("nonempty signal ID is required")
	}
	if rule, ok := canonical[s.ID()]; ok && !sameCollectUnit(s.Unit(), rule.signal.Unit()) {
		return fmt.Errorf("signal %q redefines its canonical unit", s.ID())
	}
	return nil
}

// validateCollectBatch checks cycle identity, signal ownership, and every
// observation. Duplicates are keyed by signal and target; source-scoped and
// source-local targets also key on source, since their identity is only
// meaningful within it. batch must come from proto.Clone, which drops
// typed-nil oneof wrappers, so later checks need not nil-check them.
func validateCollectBatch(batch *observation.ObservationBatch, cycleID string, owned collect.Signals) error {
	if batch.GetCollectionId() != cycleID || strings.TrimSpace(batch.GetCollectionId()) == "" {
		return fmt.Errorf("collection ID %q does not match active cycle %q", batch.GetCollectionId(), cycleID)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		return err
	}
	definitions := make(map[string]collect.Signal, len(owned))
	for _, s := range owned {
		definitions[s.ID()] = s
	}
	seen := make(map[string]bool)
	for i, o := range batch.GetObservations() {
		definition := definitions[o.GetSignalId()]
		if definition == nil {
			return fmt.Errorf("observation %d: signal %q is not owned by this sensor", i, o.GetSignalId())
		}
		if err := validateCollectObservation(o, definition); err != nil {
			return fmt.Errorf("observation %d: %w", i, err)
		}
		target, _ := proto.MarshalOptions{Deterministic: true}.Marshal(o.GetTarget())
		namespace := ""
		if o.GetTarget() == nil || collectSourceLocal(o.GetTarget()) {
			namespace = o.GetSource()
		}
		key := o.GetSignalId() + "\x00" + namespace + "\x00" + string(target)
		if seen[key] {
			return fmt.Errorf("observation %d: duplicate signal %q for one target", i, o.GetSignalId())
		}
		seen[key] = true
	}
	return nil
}

// validateCollectObservation applies HV's acceptance rules to one observation:
// provenance, a past timestamp, canonical unit, a valid target, exactly one
// outcome, and, for canonical signals, the target kind in canonicalTargets.
// Whether a value's kind fits a signal is left to the adapter that interprets
// it, so a wrong scalar fails only the checks that read it. These rules live
// here rather than in collect so its contract is unchanged for other
// consumers.
func validateCollectObservation(o *observation.Observation, s collect.Signal) error {
	if strings.TrimSpace(o.GetSource()) == "" {
		return fmt.Errorf("source is required")
	}
	at := o.GetObservedAt()
	if at == nil || (at.Seconds == 0 && at.Nanos == 0) {
		return fmt.Errorf("nonzero observation timestamp is required")
	}
	if err := at.CheckValid(); err != nil {
		return fmt.Errorf("observation timestamp: %w", err)
	}
	if at.AsTime().After(time.Now()) {
		return fmt.Errorf("observation timestamp is in the future")
	}
	if !sameCollectUnit(o.Unit, s.Unit()) {
		return fmt.Errorf("signal %q has noncanonical unit", s.ID())
	}
	for _, ref := range gpuRefs(o.GetTarget()) {
		if (strings.TrimSpace(ref.GetUuid()) == "") == (strings.TrimSpace(ref.GetSourceLocalId()) == "") {
			return fmt.Errorf("GPU target requires exactly one UUID or source-local ID")
		}
	}
	if collectSourceLocal(o.GetTarget()) && o.GetCollectionError() == nil {
		return fmt.Errorf("source-local GPU identity requires a collection error")
	}
	if entry := o.GetTarget().GetJournalEntry(); entry != nil && (strings.TrimSpace(entry.GetBootId()) == "" || strings.TrimSpace(entry.GetCursor()) == "") {
		return fmt.Errorf("journal entry target requires a boot ID and cursor")
	}
	switch outcome := o.GetOutcome().(type) {
	case *observation.Observation_Value:
		if o.GetTarget() == nil {
			return fmt.Errorf("value requires a typed target")
		}
		if outcome.Value.GetKind() == nil {
			return fmt.Errorf("value kind is required")
		}
		if d := outcome.Value.GetDoubleValue(); math.IsNaN(d) || math.IsInf(d, 0) {
			return fmt.Errorf("finite double value is required")
		}
	case *observation.Observation_CollectionError:
		category := outcome.CollectionError.GetCategory()
		if category < observation.CollectionErrorCategoryTimeout || category > observation.CollectionErrorCategorySourceError {
			return fmt.Errorf("defined collection-error category is required")
		}
	case *observation.Observation_NoValue:
		switch outcome.NoValue.GetReason() {
		case observation.NoValueReasonNoTargets:
			if o.GetTarget() != nil && !gpuParentOutcome(o, s) {
				return fmt.Errorf("NO_TARGETS must be source-scoped or identify an entity family's parent GPU")
			}
		case observation.NoValueReasonNotSupported:
			if o.GetTarget() == nil {
				return fmt.Errorf("NOT_SUPPORTED requires a target")
			}
		default:
			return fmt.Errorf("defined no-value reason is required")
		}
	default:
		return fmt.Errorf("observation outcome is required")
	}
	if rule, ok := canonical[s.ID()]; ok && o.GetTarget() != nil && targetKind(o.GetTarget()) != rule.target && !gpuParentOutcome(o, s) {
		return fmt.Errorf("signal %q requires a %s target", s.ID(), rule.target)
	}
	return nil
}

// gpuRefs lists the GPUs a target names. observation.ValidateBatch has
// already checked the target's shape.
func gpuRefs(target *observation.ObservationTarget) []*observation.GPURef {
	switch kind := target.GetKind().(type) {
	case *observation.ObservationTarget_GPU:
		return []*observation.GPURef{kind.Gpu}
	case *observation.ObservationTarget_NVLinkEndpoint:
		return []*observation.GPURef{kind.NvLinkEndpoint.GetGpu()}
	case *observation.ObservationTarget_GPUPair:
		return []*observation.GPURef{kind.GpuPair.GetSourceGpu(), kind.GpuPair.GetPeerGpu()}
	case *observation.ObservationTarget_GPUProcess:
		return []*observation.GPURef{kind.GpuProcess.GetGpu()}
	default:
		return nil
	}
}

// collectSourceLocal reports whether any GPU in target lacks a UUID.
func collectSourceLocal(target *observation.ObservationTarget) bool {
	return slices.ContainsFunc(gpuRefs(target), func(ref *observation.GPURef) bool { return ref.GetSourceLocalId() != "" })
}

// A parent GPU scopes failed entity enumeration without inventing an endpoint or process.
func gpuParentOutcome(o *observation.Observation, s collect.Signal) bool {
	rule, ok := canonical[s.ID()]
	if !ok || (rule.target != targetNVLinkEndpoint && rule.target != targetGPUProcess) || targetKind(o.GetTarget()) != targetGPU || o.GetValue() != nil {
		return false
	}
	return o.GetCollectionError() != nil || o.GetNoValue() != nil
}
