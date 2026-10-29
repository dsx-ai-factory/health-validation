// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// exportOperation names the toolkit step in a CollectionError. The toolkit's
// measurement errors do not carry the source operation that failed.
const exportOperation = "measure"

// gpuIDField is the json field that identifies a GPU-scoped measurement.
const gpuIDField = "gpu_id"

// toolkitSourcePrefix starts the source of every CollectionError
// FromMeasurements writes, which is how ToMeasurements recognizes the errors
// it exported.
const toolkitSourcePrefix = "toolkit."

// FromMeasurements converts toolkit measurements into observations, one per
// json-tagged measurement field, attributed to source "toolkit.<sensor>".
// A measurement keyed by a [health.ProvisionalEntity] or
// [health.SourceEntity] is instead attributed to its key's source, since the
// identity [ToMeasurements] rebuilds is scoped to that source; its
// CollectionErrors still name "toolkit.<sensor>".
// Each signal ID is the measurement's category and the field name the
// catalog's checks read, e.g. "nvlink.inactive_count", so the batch carries
// what the checks consume without depending on canonical collect signal
// definitions. [ToMeasurements] reverses it.
//
// A measurement with a gpu_id targets that GPU, or its source-local slot
// when provisional; a source-level failure has no target; any other
// measurement
// targets the node, or carries no target when nodeID is empty. Scalar
// fields keep their type; maps, slices, and structs are encoded as a JSON
// string. A field named by a measurement error becomes a CollectionError,
// and one the source reported unsupported becomes a NoValue, so a zero
// value is never published as an observation. Values the catalog derives
// at evaluation ([health.DerivedSignaler]) are not exported. rdma_topo
// measurements instead use canonical signal IDs, keeping findings and
// collection failures separate and writing shared invocation evidence once.
func FromMeasurements(sensor, nodeID string, observedAt time.Time, measurements ...health.Measurement) ([]*observation.Observation, error) {
	source := toolkitSourcePrefix + sensor
	at := timestamppb.New(observedAt)
	var out []*observation.Observation
	var rdmaTopoMeasurements []pcie.RDMATopoMeasurement
	for _, m := range measurements {
		if value, ok := rdmaTopoMeasurement(m); ok {
			rdmaTopoMeasurements = append(rdmaTopoMeasurements, value)
			continue
		}
		exported, err := export(source, nodeID, at, m)
		if err != nil {
			return nil, fmt.Errorf("export %s measurement: %w", sensor, err)
		}
		out = append(out, exported...)
	}
	if len(rdmaTopoMeasurements) > 0 {
		exported, err := exportRDMATopo(source, nodeID, at, rdmaTopoMeasurements)
		if err != nil {
			return nil, fmt.Errorf("export %s measurement: %w", sensor, err)
		}
		out = append(out, exported...)
	}
	return out, nil
}

func export(source, nodeID string, at *timestamppb.Timestamp, m health.Measurement) ([]*observation.Observation, error) {
	// The interface fixes only the method set: a nil value, a nil pointer,
	// or a non-struct type all satisfy it, and reflection over their fields
	// would panic.
	v := reflect.ValueOf(m)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	switch {
	case m == nil || v.Kind() == reflect.Pointer:
		return nil, fmt.Errorf("measurement %T is nil", m)
	case v.Kind() != reflect.Struct:
		return nil, fmt.Errorf("measurement %T is not a struct", m)
	}

	// A signal may be named by several error entries; each is kept, in entry
	// order, since the catalog reports every one that matches a check.
	failures := make(map[string][]error)
	for _, entry := range m.Errors() {
		for _, signal := range entry.FailedSignals() {
			failures[signal] = append(failures[signal], entry.Err())
		}
	}
	unsupported := make(map[string]bool)
	if reporter, ok := m.(health.UnsupportedSignaler); ok {
		for _, signal := range reporter.UnsupportedSignals() {
			unsupported[signal] = true
		}
	}

	target := measurementTarget(m, v, nodeID, failures)
	sensorSource := source
	if key := health.KeyOf(m); key.Provisional() {
		source = key.Source()
	}
	prefix := m.Category().String() + "."
	var out []*observation.Observation
	for i := range v.NumField() {
		field, ok := signalName(v.Type().Field(i))
		if !ok {
			continue
		}
		signalID := prefix + field
		if errs := failures[field]; len(errs) > 0 {
			out = append(out, observation.NewCollectionErrorObservation(
				signalID, nil, source, exportOperation, target, at, collectionError(sensorSource, errs)))
			continue
		}
		if unsupported[field] {
			out = append(out, observation.NewNoValueObservation(
				signalID, nil, source, target, at, &observation.NoValue{Reason: observation.NoValueReasonNotSupported}))
			continue
		}
		value, err := fieldValue(v.Field(i))
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field, err)
		}
		out = append(out, &observation.Observation{
			SignalId:   signalID,
			Source:     source,
			Target:     target,
			ObservedAt: at,
			Outcome:    &observation.Observation_Value{Value: value},
		})
	}
	return out, nil
}

// measurementTarget returns the GPU a measurement describes, or the node for
// a host-wide measurement. A provisional key targets its source-local slot,
// not gpu_id, which is only a label. A source-level failure names no GPU, and
// any other failed GPU identity lookup leaves gpu_id as a placeholder, so
// those carry no target.
func measurementTarget(m health.Measurement, v reflect.Value, nodeID string, failures map[string][]error) *observation.ObservationTarget {
	for i := range v.NumField() {
		if name, ok := signalName(v.Type().Field(i)); !ok || name != gpuIDField || v.Field(i).Kind() != reflect.String {
			continue
		}
		gpuID, key := v.Field(i).String(), health.KeyOf(m)
		switch {
		case gpuID == "" || key.SourceLevel():
			return nil
		case key.Provisional():
			return observation.GPUSourceLocalTarget(key.Slot())
		case len(failures[gpuIDField]) == 0:
			return observation.GPUTarget(gpuID)
		}
		return nil
	}
	if nodeID == "" {
		return nil
	}
	return observation.NodeTarget(nodeID)
}

// signalName mirrors how the catalog names a measurement's signals (its
// evaluation package's fieldSignalKey): the json tag, or the Go field name
// for a tag carrying only options. For example:
//
//	GPUID string `json:"gpu_id"`          → "gpu_id"
//	Since string `json:"since,omitempty"` → "since"
//	Extra int    `json:",omitempty"`      → "Extra"
//	Debug string `json:"-"`               → not a signal
//	errors []health.ErroredMeasurement    → not a signal (unexported, untagged)
func signalName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if !f.IsExported() || tag == "" || tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name, true
}

func fieldValue(v reflect.Value) (*observation.Value, error) {
	switch v.Kind() {
	case reflect.Bool:
		return boolValue(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intValue(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if v.Uint() > math.MaxInt64 {
			return nil, fmt.Errorf("unsigned value %d overflows int64", v.Uint())
		}
		return intValue(int64(v.Uint())), nil
	case reflect.Float32, reflect.Float64:
		return doubleValue(v.Float()), nil
	case reflect.String:
		return stringValue(v.String()), nil
	}
	encoded, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return stringValue(encoded), nil
}

// encodeJSON encodes a non-scalar field. A nil slice or map encodes as an
// empty one, so consumers see "[]" or "{}" rather than "null".
func encodeJSON(v reflect.Value) (string, error) {
	value := v.Interface()
	switch {
	case v.Kind() == reflect.Slice && v.IsNil():
		value = []any{}
	case v.Kind() == reflect.Map && v.IsNil():
		value = map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// collectionError folds a signal's errors into one CollectionError. The
// messages are joined as the catalog joins them in a verdict, and the
// failure is a timeout only if every error was one, since evaluation ranks
// any other failure above a timeout.
func collectionError(source string, errs []error) *observation.CollectionError {
	category := observation.CollectionErrorCategoryTimeout
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		if !errors.Is(err, health.ErrCollectionTimeout) {
			category = observation.CollectionErrorCategorySourceError
		}
		messages = append(messages, err.Error())
	}
	return &observation.CollectionError{Source: source, Category: category, Detail: strings.Join(messages, "; ")}
}

func doubleValue(v float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: v}}
}

func intValue(v int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}
}

func boolValue(v bool) *observation.Value {
	return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}}
}

func stringValue(v string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: v}}
}
