// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// errNoObservation marks catalog signals no observation in the batch fed.
var errNoObservation = errors.New("no observation was collected for these signals")

// ToMeasurements rebuilds the catalog's measurements from observations, one
// per measurement type and entity: a GPU, an InfiniBand port, or the node
// for host-wide types. A GPU is identified by UUID; one reported only by a
// source-local slot is "<source>:<slot>", so slots from different sources
// stay distinct. A port is "<device>/<port>".
//
// An observation is read as one of two shapes:
//   - <domain>.<field>, as written by [FromMeasurements], sets that field
//     of the domain's measurement type.
//   - A mapped collect signal ID sets the catalog fields the mapping
//     table assigns it, as during migration to collect sensors.
//
// Both shapes may appear in one batch and feed one measurement. Other
// signals are ignored. Collection failures attach to the affected catalog
// signals, so unrelated checks still evaluate. A catalog signal nothing fed,
// or fed twice, is reported failed rather than left at its zero value, so
// its checks cannot pass on data that was never collected. A domain with no
// observations yields no measurement, so its checks are skipped as missing.
func ToMeasurements(observations []*observation.Observation) []health.Measurement {
	b := &builder{entities: make(map[entityKey]*entity)}
	for _, current := range observations {
		b.add(current)
	}
	return b.measurements()
}

// measurementTypes are the measurement types ToMeasurements rebuilds. Types
// sharing a domain carry disjoint signals, as the catalog requires, so a
// <domain>.<field> signal still identifies its type.
var measurementTypes = []health.Measurement{
	gpu.GPUMeasurement{},
	nvlink.NVLinkMeasurement{},
	cpu.CPUMeasurement{},
	node.NodeMeasurement{},
	xid.XidMeasurement{},
	infiniband.LinkFlapMeasurement{},
	infiniband.PortMeasurement{},
}

// identity is how a measurement type names the entity it describes: the
// target an observation must carry and the fields seeded from it.
type identity int

const (
	identityNode   identity = iota // host-wide; the node is implicit
	identityGPU                    // gpu_id, from a GPU-bearing target
	identityIBPort                 // ib_device and ib_port, from an InfiniBand target
)

// schema describes one measurement type for ingestion.
type schema struct {
	typ      reflect.Type
	fields   map[string]int
	category string
	order    []string
	identity identity
}

var schemas = sync.OnceValue(func() []*schema {
	out := make([]*schema, 0, len(measurementTypes))
	owner := make(map[string]reflect.Type)
	for _, m := range measurementTypes {
		s := &schema{typ: reflect.TypeOf(m), category: m.Category().String(), fields: make(map[string]int)}
		for i := range s.typ.NumField() {
			name, ok := signalName(s.typ.Field(i))
			if !ok {
				continue
			}
			key := s.category + "." + name
			if other, dup := owner[key]; dup {
				panic(fmt.Sprintf("observations: signal %q is carried by both %s and %s", key, other, s.typ))
			}
			owner[key] = s.typ
			s.fields[name] = i
			s.order = append(s.order, name)
		}
		switch {
		case s.has(gpuIDField):
			s.identity = identityGPU
		case s.has(infiniband.SignalIBDevice) && s.has(infiniband.SignalIBPort):
			s.identity = identityIBPort
		}
		out = append(out, s)
	}
	return out
})

func (s *schema) has(field string) bool {
	_, ok := s.fields[field]
	return ok
}

// identityFields are the fields seeded from the target; a target that
// cannot be identified fails them along with the observed signal.
func (s *schema) identityFields() []string {
	switch s.identity {
	case identityGPU:
		return []string{gpuIDField}
	case identityIBPort:
		return []string{infiniband.SignalIBDevice, infiniband.SignalIBPort}
	}
	return nil
}

func schemaFor(m health.Measurement) *schema {
	for _, s := range schemas() {
		if s.typ == reflect.TypeOf(m) {
			return s
		}
	}
	panic(fmt.Sprintf("observations: %T is not a registered measurement type", m))
}

// schemaForField returns domain's measurement type carrying field, or nil.
func schemaForField(domain, field string) *schema {
	for _, s := range schemas() {
		if s.category == domain && s.has(field) {
			return s
		}
	}
	return nil
}

// neutralFields returns the <domain>.<field> signals whose unsupported
// NoValue leaves the field at its zero value: the catalog fields of the
// mapped signals marked unsupportedIsNeutral, which are the signals the
// toolkit's sensors treat as neutral. Any other unsupported field fails, so
// a batch from another producer cannot pass a check on a missing value.
var neutralFields = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool)
	for _, mapped := range mappings {
		if !mapped.unsupportedIsNeutral {
			continue
		}
		for _, field := range mapped.catalog {
			out[schemaFor(mapped.measurement).category+"."+field] = true
		}
	}
	return out
})

// feed is how one observation writes into a measurement.
type feed struct {
	schema  *schema
	apply   func(measurement any, o *observation.Observation) error
	unit    string
	catalog []string
	neutral bool
}

// resolve returns how current feeds a measurement, or false for a signal no
// measurement type reads.
func resolve(current *observation.Observation) (feed, bool) {
	if domain, field, ok := strings.Cut(current.GetSignalId(), "."); ok {
		if s := schemaForField(domain, field); s != nil {
			return feed{schema: s, catalog: []string{field}, neutral: neutralFields()[current.GetSignalId()]}, true
		}
	}
	for _, mapped := range mappings {
		if mapped.signal.ID() == current.GetSignalId() {
			return feed{
				schema:  schemaFor(mapped.measurement),
				apply:   mapped.apply,
				catalog: mapped.catalog,
				unit:    mapped.unit,
				neutral: mapped.unsupportedIsNeutral,
			}, true
		}
	}
	return feed{}, false
}

type entityKey struct {
	schema *schema
	id     string
}

// entity accumulates one measurement.
type entity struct {
	fed      map[string]bool
	errIndex map[string]int
	// idFromTarget holds the identity fields fed so far only by the target,
	// so an explicit observation of one is not a second feed.
	idFromTarget map[string]bool
	value        reflect.Value // addressable measurement struct
	unsupported  []string
	errs         []entityError
}

type entityError struct {
	err    error
	fields []string
}

type builder struct {
	entities map[entityKey]*entity
	order    []entityKey
}

func (b *builder) add(current *observation.Observation) {
	f, ok := resolve(current)
	if !ok {
		return
	}
	id, targetErr := targetID(f.schema, current)
	e := b.entity(f.schema, id)

	for _, field := range f.catalog {
		if e.fed[field] && !e.idFromTarget[field] {
			e.addError(fmt.Errorf("signal %q was observed more than once", current.GetSignalId()), f.catalog...)
			return
		}
	}
	for _, field := range f.catalog {
		e.fed[field] = true
		delete(e.idFromTarget, field)
	}

	switch {
	case targetErr != nil:
		e.addError(targetErr, append(f.schema.identityFields(), f.catalog...)...)
	case current.GetCollectionError() != nil:
		e.addError(collectionFailure(current.GetSource(), current.GetCollectionError()), f.catalog...)
	case current.GetNoValue() != nil:
		if f.neutral && current.GetNoValue().GetReason() == observation.NoValueReasonNotSupported {
			e.unsupported = append(e.unsupported, f.catalog...)
			return
		}
		e.addError(noValueFailure(current.GetSource(), current.GetNoValue()), f.catalog...)
	default:
		if err := e.apply(f, current); err != nil {
			e.addError(err, f.catalog...)
		}
	}
}

// targetID returns the entity an observation describes under s's identity:
// a GPU, an InfiniBand port, or the node. An empty ID groups observations
// that carry no identity, such as a whole-source failure.
func targetID(s *schema, current *observation.Observation) (string, error) {
	switch s.identity {
	case identityGPU:
		return gpuTargetID(current)
	case identityIBPort:
		return ibTargetID(current)
	}
	return current.GetTarget().GetNode().GetId(), nil
}

func gpuTargetID(current *observation.Observation) (string, error) {
	target := current.GetTarget()
	var ref *observation.GPURef
	switch {
	case target == nil:
		if current.GetValue() != nil {
			return "", fmt.Errorf("signal %q has no GPU target", current.GetSignalId())
		}
		return "", nil
	case target.GetGpu() != nil:
		ref = target.GetGpu()
	case target.GetNvLinkEndpoint() != nil:
		ref = target.GetNvLinkEndpoint().GetGpu()
	case target.GetGpuPair() != nil:
		ref = target.GetGpuPair().GetSourceGpu()
	default:
		return "", fmt.Errorf("signal %q does not identify a GPU target", current.GetSignalId())
	}
	if uuid := ref.GetUuid(); uuid != "" {
		return uuid, nil
	}
	// A source-local slot keeps a failed UUID lookup attributable to one GPU,
	// but only within the source that numbered it: two sources' slot 0 are
	// not known to be the same GPU, so the slot is qualified by its source.
	slot := ref.GetSourceLocalId()
	switch {
	case slot == "":
		return "", fmt.Errorf("signal %q has no GPU identifier", current.GetSignalId())
	case current.GetSource() == "":
		return "", fmt.Errorf("signal %q has source-local GPU %q but no source", current.GetSignalId(), slot)
	}
	return current.GetSource() + ":" + slot, nil
}

// ibTargetID returns "<device>/<port>" for a port target, or "<device>" for
// a device-scoped one such as a failure listing a device's ports.
func ibTargetID(current *observation.Observation) (string, error) {
	target := current.GetTarget()
	var name string
	switch {
	case target == nil:
		if current.GetValue() != nil {
			return "", fmt.Errorf("signal %q has no InfiniBand target", current.GetSignalId())
		}
		return "", nil
	case target.GetInfiniBandPort() != nil:
		name = target.GetInfiniBandPort().GetDevice().GetName()
		if name != "" {
			name = fmt.Sprintf("%s/%d", name, target.GetInfiniBandPort().GetPort())
		}
	case target.GetInfiniBandDevice() != nil:
		name = target.GetInfiniBandDevice().GetName()
	default:
		return "", fmt.Errorf("signal %q does not identify an InfiniBand port", current.GetSignalId())
	}
	if name == "" {
		return "", fmt.Errorf("signal %q has no InfiniBand device name", current.GetSignalId())
	}
	return name, nil
}

func (b *builder) entity(s *schema, id string) *entity {
	key := entityKey{schema: s, id: id}
	if e := b.entities[key]; e != nil {
		return e
	}
	e := &entity{value: reflect.New(s.typ).Elem(), fed: make(map[string]bool), errIndex: make(map[string]int), idFromTarget: make(map[string]bool)}
	switch s.identity {
	case identityGPU:
		if id != "" {
			e.value.Field(s.fields[gpuIDField]).SetString(id)
			e.seed(gpuIDField)
		} else {
			// Matches the toolkit sensors, which name a GPU they could not
			// identify after their domain.
			e.value.Field(s.fields[gpuIDField]).SetString(s.category)
		}
	case identityIBPort:
		// An unidentified port keeps an empty device, which EntityID omits.
		if id != "" {
			device, port, _ := strings.Cut(id, "/")
			e.value.Field(s.fields[infiniband.SignalIBDevice]).SetString(device)
			e.seed(infiniband.SignalIBDevice)
			if n, err := strconv.ParseInt(port, 10, 64); err == nil {
				e.value.Field(s.fields[infiniband.SignalIBPort]).SetInt(n)
			}
			e.seed(infiniband.SignalIBPort)
		}
	}
	b.entities[key] = e
	b.order = append(b.order, key)
	return e
}

// seed records an identity field filled from the target.
func (e *entity) seed(field string) {
	e.fed[field] = true
	e.idFromTarget[field] = true
}

// apply writes a value observation into the measurement.
func (e *entity) apply(f feed, current *observation.Observation) error {
	if f.unit != "" && current.GetUnit() != f.unit {
		return fmt.Errorf("signal %q has unit %q, expected %q", current.GetSignalId(), current.GetUnit(), f.unit)
	}
	if f.apply != nil {
		return f.apply(e.value.Addr().Interface(), current)
	}
	if len(f.catalog) != 1 {
		return fmt.Errorf("signal %q feeds %d fields and needs an apply function", current.GetSignalId(), len(f.catalog))
	}
	return setField(e.value.Field(f.schema.fields[f.catalog[0]]), current)
}

// setField assigns an observed value to a measurement field of a matching
// kind. A map, slice, or struct field reads a JSON string; an empty map or
// slice decodes as nil, its zero value, since export writes nil as empty.
func setField(field reflect.Value, current *observation.Observation) error {
	mismatch := fmt.Errorf("signal %q value %v does not fit a %s field", current.GetSignalId(), current.GetValue(), field.Type())
	switch kind := current.GetValue().GetKind().(type) {
	case *observation.Value_BoolValue:
		if field.Kind() != reflect.Bool {
			return mismatch
		}
		field.SetBool(kind.BoolValue)
	case *observation.Value_IntValue:
		switch {
		case field.CanInt() && !field.OverflowInt(kind.IntValue):
			field.SetInt(kind.IntValue)
		case field.CanUint() && kind.IntValue >= 0 && !field.OverflowUint(uint64(kind.IntValue)):
			field.SetUint(uint64(kind.IntValue))
		case field.CanFloat():
			field.SetFloat(float64(kind.IntValue))
		default:
			return mismatch
		}
	case *observation.Value_UintValue:
		switch {
		case field.CanUint() && !field.OverflowUint(kind.UintValue):
			field.SetUint(kind.UintValue)
		case field.CanInt() && kind.UintValue <= uint64(^uint64(0)>>1) && !field.OverflowInt(int64(kind.UintValue)):
			field.SetInt(int64(kind.UintValue))
		case field.CanFloat():
			field.SetFloat(float64(kind.UintValue))
		default:
			return mismatch
		}
	case *observation.Value_DoubleValue:
		if !field.CanFloat() {
			return mismatch
		}
		field.SetFloat(kind.DoubleValue)
	case *observation.Value_StringValue:
		switch field.Kind() {
		case reflect.String:
			field.SetString(kind.StringValue)
		case reflect.Map, reflect.Slice, reflect.Struct, reflect.Pointer:
			if err := json.Unmarshal([]byte(kind.StringValue), field.Addr().Interface()); err != nil {
				return fmt.Errorf("signal %q: decode %s: %w", current.GetSignalId(), field.Type(), err)
			}
			if (field.Kind() == reflect.Map || field.Kind() == reflect.Slice) && field.Len() == 0 {
				field.SetZero()
			}
		default:
			return mismatch
		}
	default:
		return mismatch
	}
	return nil
}

// addError records a failure, merging failures with the same message so a
// check reading several of the fields reports the cause once.
func (e *entity) addError(err error, fields ...string) {
	key := fmt.Sprintf("%t\x00%s", errors.Is(err, health.ErrCollectionTimeout), err.Error())
	if i, ok := e.errIndex[key]; ok {
		for _, field := range fields {
			if !slices.Contains(e.errs[i].fields, field) {
				e.errs[i].fields = append(e.errs[i].fields, field)
			}
		}
		return
	}
	e.errIndex[key] = len(e.errs)
	e.errs = append(e.errs, entityError{err: err, fields: slices.Clone(fields)})
}

func (b *builder) measurements() []health.Measurement {
	keys := slices.Clone(b.order)
	slices.SortStableFunc(keys, func(a, b entityKey) int {
		if a.schema != b.schema {
			return slices.Index(schemas(), a.schema) - slices.Index(schemas(), b.schema)
		}
		return strings.Compare(a.id, b.id)
	})

	out := make([]health.Measurement, 0, len(keys))
	for _, key := range keys {
		e := b.entities[key]
		var missing []string
		for _, field := range key.schema.order {
			if !e.fed[field] {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			e.addError(errNoObservation, missing...)
		}

		ptr := e.value.Addr().Interface()
		adder := ptr.(interface{ AddError(error, ...string) })
		for _, failure := range e.errs {
			adder.AddError(failure.err, failure.fields...)
		}
		if marker, ok := ptr.(interface{ MarkUnsupported(...string) }); ok && len(e.unsupported) > 0 {
			marker.MarkUnsupported(e.unsupported...)
		}
		out = append(out, e.value.Interface().(health.Measurement))
	}
	return out
}

// restoredError carries a collection failure's message and whether it was a
// timeout, which decides whether evaluation recommends a retry.
type restoredError struct {
	msg     string
	timeout bool
}

func (e restoredError) Error() string { return e.msg }

func (e restoredError) Is(target error) bool {
	return e.timeout && target == health.ErrCollectionTimeout
}

// collectionFailure turns a CollectionError back into a measurement error.
// A failure FromMeasurements exported carries the original message as its
// detail, so it is restored verbatim; any other names its source.
func collectionFailure(source string, failure *observation.CollectionError) error {
	timeout := failure.GetCategory() == observation.CollectionErrorCategoryTimeout
	if strings.HasPrefix(source, toolkitSourcePrefix) && failure.GetOperation() == exportOperation && failure.GetDetail() != "" {
		return restoredError{msg: failure.GetDetail(), timeout: timeout}
	}
	msg := fmt.Sprintf("%s operation %q failed with %s", failure.GetSource(), failure.GetOperation(), failure.GetCategory())
	if failure.GetSourceError() != "" {
		msg += fmt.Sprintf(" (%s)", failure.GetSourceError())
	}
	if failure.GetDetail() != "" {
		msg += ": " + failure.GetDetail()
	}
	return restoredError{msg: msg, timeout: timeout}
}

func noValueFailure(source string, noValue *observation.NoValue) error {
	detail := fmt.Sprintf("%s returned no value (%s)", source, noValue.GetReason())
	if noValue.GetSourceStatus() != "" {
		detail += fmt.Sprintf(" (%s)", noValue.GetSourceStatus())
	}
	if noValue.GetDetail() != "" {
		detail += ": " + noValue.GetDetail()
	}
	return errors.New(detail)
}
