// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// Entity holds the identity and collection failures a [GPUConverter] records
// on a measurement. Embed it in a per-GPU measurement type, which then needs
// only its Category and an EntityID returning its gpu_id field.
type Entity struct {
	key  health.EntityKey
	errs []health.ErroredMeasurement
}

// AddError records that signals could not be collected because of err.
func (e *Entity) AddError(err error, signals ...string) {
	e.errs = append(e.errs, health.NewErroredMeasurement(err, signals...))
}

// SetEntityKey records the identity evaluation keys the measurement by.
func (e *Entity) SetEntityKey(key health.EntityKey) { e.key = key }

// EntityKey implements [health.EntityKeyed].
func (e Entity) EntityKey() health.EntityKey { return e.key }

// Errors implements part of [health.Measurement].
func (e Entity) Errors() []health.ErroredMeasurement { return e.errs }

// GPUConverter builds one measurement of a caller's type per GPU from
// collect observations, grouping them and reporting failures as
// [ToMeasurements] does: a GPU read by UUID is a verified entity, one known
// only by a source-local slot is provisional, a whole-source failure fails
// the fields it would have fed on every GPU, and a field nothing fed, or a
// signal reported unsupported, fails its checks rather than reading as zero.
type GPUConverter struct {
	schema *schema
	fields map[string]string
}

// NewGPUConverter returns a converter building M from the collect signals in
// fields, which maps each signal ID to the json-tagged field of M it sets.
// M must be a struct with a string field tagged `json:"gpu_id"`, whose other
// json-tagged fields are the catalog signals its checks read, each fed by one
// signal in fields. M must have an EntityID method returning its gpu_id, and
// *M must have AddError and SetEntityKey, as embedding [Entity] by value
// provides.
func NewGPUConverter[M health.Measurement](fields map[string]string) (*GPUConverter, error) {
	var zero M
	typ := reflect.TypeOf(zero)
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("measurement type %v is not a struct", typ)
	}
	if _, ok := any(&zero).(interface {
		AddError(error, ...string)
		SetEntityKey(health.EntityKey)
	}); !ok {
		return nil, fmt.Errorf("*%v needs AddError and SetEntityKey; embed observations.Entity", typ)
	}
	// A converter fills a zero M, so an embedded pointer is nil when its
	// promoted AddError or SetEntityKey runs.
	for i := range typ.NumField() {
		if f := typ.Field(i); f.Anonymous && f.Type.Kind() == reflect.Pointer {
			return nil, fmt.Errorf("%v embeds %v by pointer; embed it by value", typ, f.Type)
		}
	}
	s := newSchema(zero)
	if s.gpuID < 0 || typ.Field(s.gpuID).Type.Kind() != reflect.String {
		return nil, fmt.Errorf("%v has no string field tagged json:%q", typ, gpuIDField)
	}
	// Without EntityID a failing check cannot name its GPU.
	if _, ok := any(zero).(health.EntityIdentifier); !ok {
		return nil, fmt.Errorf("%v needs an EntityID method returning its %s field", typ, gpuIDField)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no signals map to %v", typ)
	}
	// One field per signal: a second signal into the same field would error
	// that field on every GPU at collection time.
	fedBy := make(map[string]string, len(fields))
	for _, signal := range slices.Sorted(maps.Keys(fields)) {
		field := fields[signal]
		if _, ok := s.fields[field]; !ok || field == gpuIDField {
			return nil, fmt.Errorf("signal %q maps to %q, which is not a catalog signal of %v", signal, field, typ)
		}
		if other, ok := fedBy[field]; ok {
			return nil, fmt.Errorf("signals %q and %q both map to field %q of %v; map each field from one signal", other, signal, field, typ)
		}
		fedBy[field] = signal
	}
	// A field no signal feeds would fail its checks on every GPU, every run.
	for _, field := range s.order {
		if _, ok := fedBy[field]; !ok && field != gpuIDField {
			return nil, fmt.Errorf("no signal maps to field %q of %v; map one or drop its json tag", field, typ)
		}
	}
	return &GPUConverter{schema: s, fields: maps.Clone(fields)}, nil
}

// Category is the measurement type's category.
func (c *GPUConverter) Category() health.Category {
	return reflect.Zero(c.schema.typ).Interface().(health.Measurement).Category()
}

// Feeds maps each signal ID to the catalog signals it fills, as
// [MappedFeeds] does for the built-in types.
func (c *GPUConverter) Feeds() map[string][]string {
	out := make(map[string][]string, len(c.fields))
	for signal, field := range c.fields {
		out[signal] = []string{field}
	}
	return out
}

// ToMeasurements builds one measurement per GPU from the observations of the
// converter's signals, ignoring the rest.
func (c *GPUConverter) ToMeasurements(observations []*observation.Observation) []health.Measurement {
	b := &builder{entities: make(map[entityKey]*entity), schemas: []*schema{c.schema}}
	for _, current := range observations {
		if field, ok := c.fields[current.GetSignalId()]; ok {
			b.add(current, feed{schema: c.schema, catalog: []string{field}, mapped: true})
		}
	}
	return b.measurements()
}

// Errored returns one measurement failing every mapped field with err, keyed
// as a whole-source failure of source, for when nothing could be collected.
func (c *GPUConverter) Errored(source string, err error) health.Measurement {
	v := reflect.New(c.schema.typ)
	v.Elem().Field(c.schema.gpuID).SetString(c.schema.category)
	ptr := v.Interface().(interface {
		AddError(error, ...string)
		SetEntityKey(health.EntityKey)
	})
	ptr.SetEntityKey(health.SourceEntity(source))
	ptr.AddError(err, slices.Compact(slices.Sorted(maps.Values(c.fields)))...)
	return v.Elem().Interface().(health.Measurement)
}
