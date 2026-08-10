package evaluation

import (
	"reflect"
	"strings"
)

// ToSignals converts a typed measurement struct into the map[string]any
// that CEL evaluation expects. Keys are derived from json struct tags.
func ToSignals(m any) map[string]any {
	v := reflect.ValueOf(m)
	t := v.Type()

	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key, _, _ := strings.Cut(tag, ",")
		out[key] = v.Field(i).Interface()
	}
	return out
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
