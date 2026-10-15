// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package evaluation

import (
	"reflect"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// ToSignals converts a typed measurement struct, or a pointer to one,
// into the map[string]any that CEL evaluation expects. Keys are derived
// from json struct tags, plus anything the measurement computes via
// [health.DerivedSignaler].
func ToSignals(m any) map[string]any {
	v := reflect.Indirect(reflect.ValueOf(m))
	t := v.Type()

	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		key, ok := fieldSignalKey(t.Field(i))
		if !ok {
			continue
		}
		out[key] = v.Field(i).Interface()
	}
	if d, ok := m.(health.DerivedSignaler); ok {
		for key, value := range d.DerivedSignals() {
			out[key] = value
		}
	}
	return out
}

// fieldSignalKey returns the signal name f's json tag declares, and
// ok=false when f carries no tag or opts out with "-" and so is not part
// of the signal schema at all.
func fieldSignalKey(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return "", false
	}
	key, _, _ := strings.Cut(tag, ",")
	if key == "" {
		// A tag carrying only options (`json:",omitempty"`) keeps the Go
		// field name, as encoding/json does.
		key = f.Name
	}
	return key, true
}

// FieldKeys returns only the signal names a measurement type's own
// json-tagged fields declare, excluding anything [health.DerivedSignaler]
// contributes. [SchemaKeys] folds the two together because routing does
// not care where a signal came from; telling them apart is what lets a
// caller detect a derived name shadowing a field.
func FieldKeys(m any) []string {
	t := reflect.TypeOf(m)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		if key, ok := fieldSignalKey(t.Field(i)); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// SchemaKeys returns the signal names a measurement type's schema carries
// — the json-tagged field names [ToSignals] would emit. Routing and the
// signal-uniqueness check reason about a measurement type's schema
// through it, from a zero value rather than a populated instance.
func SchemaKeys(m any) []string {
	signals := ToSignals(m)
	keys := make([]string, 0, len(signals))
	for k := range signals {
		keys = append(keys, k)
	}
	return keys
}
