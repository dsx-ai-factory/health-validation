// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"errors"
	"fmt"
	"strings"
)

type entityKind uint8

const (
	entityUnidentified entityKind = iota
	entityNode
	entityVerified
	entityProvisional
	entitySource
)

// EntityKey is the stable identity of the entity a measurement describes.
// Evaluation rejects two measurements with one key that feed the same check:
// it cannot tell which provider to believe, so it reports neither.
//
// Construct a key with [NodeEntity], [VerifiedEntity], [ProvisionalEntity], or
// [SourceEntity]. The zero value is an unidentified entity: a per-entity
// measurement whose producer could not name it. Like a provisional key, it may
// only report the collection failure that left it unnamed; collected values
// under it are rejected, since nothing shows they describe a GPU no other
// provider reports. Two unidentified measurements for one check are rejected
// too, since nothing tells them apart.
type EntityKey struct {
	source string
	id     string
	kind   entityKind
}

// NodeEntity is the node under evaluation. A run evaluates one node, so
// host-wide measurements share this key whatever node ID their producer
// recorded.
func NodeEntity() EntityKey { return EntityKey{kind: entityNode} }

// VerifiedEntity is an entity identified by a stable hardware identity such
// as a GPU UUID. Two sources reporting the same ID describe the same entity.
func VerifiedEntity(id string) EntityKey { return EntityKey{kind: entityVerified, id: id} }

// ProvisionalEntity is an entity whose identity collection failed, named by
// the slot its source enumerated it at. Slot numbering belongs to the source,
// so the same slot from two sources is two provisional entities, never one:
// equal indexes do not prove one physical device. Both values are required.
//
// A provisional identity cannot join data across sources, so a measurement
// keyed this way must report a collection failure for every check it feeds.
func ProvisionalEntity(source, slot string) EntityKey {
	return EntityKey{kind: entityProvisional, source: source, id: slot}
}

// SourceEntity stands for whatever entities source failed to enumerate, as
// when a whole sensor timed out. Like [ProvisionalEntity], it identifies no
// physical device and must report a collection failure for every check it
// feeds.
func SourceEntity(source string) EntityKey { return EntityKey{kind: entitySource, source: source} }

// Unidentified reports whether k is the zero value.
func (k EntityKey) Unidentified() bool { return k.kind == entityUnidentified }

// Provisional reports whether k names no verified entity: a
// [ProvisionalEntity] or [SourceEntity] key.
func (k EntityKey) Provisional() bool {
	return k.kind == entityProvisional || k.kind == entitySource
}

// SourceLevel reports whether k is a [SourceEntity] key, which stands for a
// failed source rather than any one entity.
func (k EntityKey) SourceLevel() bool { return k.kind == entitySource }

// Source returns the source of a [ProvisionalEntity] or [SourceEntity] key,
// and "" for any other.
func (k EntityKey) Source() string { return k.source }

// Slot returns the slot of a [ProvisionalEntity] key, and "" for any other.
func (k EntityKey) Slot() string {
	if k.kind != entityProvisional {
		return ""
	}
	return k.id
}

// Validate reports a key built from an empty value, which cannot be told
// apart from another producer's empty value.
func (k EntityKey) Validate() error {
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	switch {
	case k.kind == entityVerified && blank(k.id):
		return errors.New("verified entity requires an ID")
	case k.kind == entityProvisional && (blank(k.source) || blank(k.id)):
		return fmt.Errorf("provisional entity requires a source and a slot, got source %q slot %q", k.source, k.id)
	case k.kind == entitySource && blank(k.source):
		return errors.New("source-level entity requires a source")
	}
	return nil
}

// String names k for error messages.
func (k EntityKey) String() string {
	switch k.kind {
	case entityNode:
		return "node"
	case entityVerified:
		return fmt.Sprintf("entity %q", k.id)
	case entityProvisional:
		return fmt.Sprintf("source %q slot %q", k.source, k.id)
	case entitySource:
		return fmt.Sprintf("source %q (no entity identified)", k.source)
	default:
		return "unidentified entity"
	}
}

// EntityKeyed is an optional interface a measurement implements to declare
// its [EntityKey].
//
// A measurement that does not implement it is keyed for compatibility: by
// [EntityIdentifier.EntityID] as a [VerifiedEntity] when that is non-empty,
// as unidentified when it is empty, and as [NodeEntity] when the type does not
// implement EntityIdentifier at all. An empty EntityID is accepted only on a
// measurement reporting a collection failure; one carrying values must name
// its entity. Implement EntityKeyed when an ID is not a stable hardware
// identity, such as a source-local slot.
type EntityKeyed interface {
	EntityKey() EntityKey
}

// KeyOf returns the [EntityKey] evaluation uses for m.
func KeyOf(m Measurement) EntityKey {
	if keyed, ok := m.(EntityKeyed); ok {
		return keyed.EntityKey()
	}
	if identified, ok := m.(EntityIdentifier); ok {
		if id := identified.EntityID(); id != "" {
			return VerifiedEntity(id)
		}
		return EntityKey{}
	}
	return NodeEntity()
}

// ErrAmbiguousMeasurement marks an evaluation that stopped because its
// measurements did not identify one entity per check unambiguously. Match it
// with [errors.Is]; [AmbiguousMeasurementError] carries the details.
var ErrAmbiguousMeasurement = errors.New("ambiguous measurements")

// AmbiguousMeasurementError reports the check and entity whose measurements
// could not be evaluated. No verdict is produced for the run: choosing one
// provider's measurement, or reporting each, would present a guess as a
// finding.
type AmbiguousMeasurementError struct {
	CheckID string
	// Reason says what made the input ambiguous.
	Reason string
	// Types lists the Go types of the offending measurements.
	Types  []string
	Entity EntityKey
}

func (e *AmbiguousMeasurementError) Error() string {
	return fmt.Sprintf("%v: check %q, %s: %s (%s)",
		ErrAmbiguousMeasurement, e.CheckID, e.Entity, e.Reason, strings.Join(e.Types, ", "))
}

// Is matches [ErrAmbiguousMeasurement].
func (e *AmbiguousMeasurementError) Is(target error) bool { return target == ErrAmbiguousMeasurement }
