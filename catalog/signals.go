package catalog

import (
	"cmp"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
)

// CheckSignalUniqueness verifies that within each Category no signal name
// is carried by more than one measurement type. This is the invariant
// that makes signal-subset routing unambiguous: a check's RequiredSignals
// then identify exactly one measurement type per category, so evaluation
// never routes a check to a measurement it was not written for.
//
// Pass one instance — the zero value is fine — of every measurement type
// the catalog consumes, both the built-ins and any a consumer adds. It
// returns an error naming the first collision found. Consumers that
// extend the catalog with their own measurement types should assert on it
// in a test so the invariant is enforced at build time.
func CheckSignalUniqueness(measurements ...health.Measurement) error {
	owners := make(map[health.Category]map[string]string)
	for _, m := range measurements {
		cat := m.Category()
		typeName := measurementTypeName(m)
		byCategory := owners[cat]
		if byCategory == nil {
			byCategory = make(map[string]string)
			owners[cat] = byCategory
		}
		for _, sig := range evaluation.SchemaKeys(m) {
			if prev, ok := byCategory[sig]; ok && prev != typeName {
				return fmt.Errorf(
					"signal %q in category %s is carried by both %s and %s; signal names must be "+
						"unique within a category so a check routes to exactly one measurement type",
					sig, cat, prev, typeName)
			}
			byCategory[sig] = typeName
		}
	}
	return nil
}

// measurementTypeName names m's struct type the same way whether m is
// passed by value or by pointer, so a type is never reported as
// colliding with itself.
func measurementTypeName(m health.Measurement) string {
	t := reflect.TypeOf(m)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.String()
}

// CheckDerivedSignals verifies that no measurement implementing
// [health.DerivedSignaler] names a derived signal after one of its own
// json-tagged fields. A derived signal takes precedence when the two
// collide, so the field would be silently replaced by the derived value
// rather than the conflict being reported.
//
// Pass one instance — the zero value is fine — of every measurement type
// the catalog consumes; types that do not derive signals are skipped. It
// returns an error naming the first collision found. Enforces one half of
// the interface's documented contract; the other half, that the derived
// key set does not vary with the data, needs a populated instance and so
// lives in [CheckDerivedSignalKeysStable].
func CheckDerivedSignals(measurements ...health.Measurement) error {
	for _, m := range measurements {
		d, ok := m.(health.DerivedSignaler)
		if !ok {
			continue
		}
		fields := make(map[string]struct{})
		for _, key := range evaluation.FieldKeys(m) {
			fields[key] = struct{}{}
		}
		for _, name := range slices.Sorted(maps.Keys(d.DerivedSignals())) {
			if _, clash := fields[name]; clash {
				return fmt.Errorf(
					"%T derives a signal named %q, which is also one of its json fields; the derived "+
						"value would silently shadow the field", m, name)
			}
		}
	}
	return nil
}

// CheckDerivedSignalKeysStable verifies that a measurement's derived
// signal names do not depend on its data: the populated instance passed
// in must advertise exactly the keys a zero value of the same type does.
// Routing and [CheckSignalUniqueness] ask a zero value which signals a
// type carries, so a key appearing only once some field is populated
// would be invisible to them and any check requiring it would never be
// routed a measurement.
//
// Unlike [CheckDerivedSignals] this needs a *populated* instance, one
// exercising whatever the derivation reads — only the caller knows what
// that looks like for its own type. The zero value to compare against is
// derived here. Measurements that do not derive signals pass trivially.
func CheckDerivedSignalKeysStable(populated health.Measurement) error {
	d, ok := populated.(health.DerivedSignaler)
	if !ok {
		return nil
	}
	zero, ok := zeroValueOf(populated).(health.DerivedSignaler)
	if !ok {
		// Unreachable for a value receiver, and for a pointer receiver
		// the zero value is a nil pointer we must not call.
		return fmt.Errorf("%T derives signals but its zero value does not; pass a value type", populated)
	}

	zeroKeys := derivedKeySet(zero)
	for _, name := range slices.Sorted(maps.Keys(d.DerivedSignals())) {
		if _, ok := zeroKeys[name]; !ok {
			return fmt.Errorf(
				"%T derives signal %q only when populated; a zero value must advertise it too, or no "+
					"check requiring it is ever routed a measurement", populated, name)
		}
	}
	populatedKeys := derivedKeySet(d)
	for _, name := range slices.Sorted(maps.Keys(zeroKeys)) {
		if _, ok := populatedKeys[name]; !ok {
			return fmt.Errorf(
				"%T derives signal %q only when empty; the key set must not vary with the data",
				populated, name)
		}
	}
	return nil
}

// derivedKeySet is d's derived signal names as a set.
func derivedKeySet(d health.DerivedSignaler) map[string]struct{} {
	out := make(map[string]struct{})
	for name := range d.DerivedSignals() {
		out[name] = struct{}{}
	}
	return out
}

// zeroValueOf returns the zero value of m's dynamic type. A pointer type
// yields a pointer to a zero struct rather than a nil pointer, since the
// caller goes on to invoke a method on it.
func zeroValueOf(m any) any {
	t := reflect.TypeOf(m)
	if t.Kind() == reflect.Pointer {
		return reflect.New(t.Elem()).Interface()
	}
	return reflect.New(t).Elem().Interface()
}

// measurementSchema is a measurement type's routing surface: its category
// and the set of signal names its struct schema carries.
type measurementSchema struct {
	keys     map[string]struct{}
	category health.Category
}

// CheckCoverage verifies that every registered check is feedable: at
// least one of the provided measurement types shares the check's Category
// and carries all of its RequiredSignals. A check with no such provider
// can never be routed a live measurement, so it can never yield a real
// verdict — at runtime it is a perpetual NoApplicableMeasurement skip
// (when its category is otherwise collected) or no verdict at all (when
// its category is never collected). Either way it is dead weight, so this
// turns that latent authoring gap (a renamed signal, or a check
// registered without the measurement type that feeds it) into an error at
// test time.
//
// Pass one instance — the zero value is fine — of every measurement type
// the catalog's sensors produce. It returns an error naming the first
// uncovered check, in ID order for a stable message. Consumers typically
// assert on it in a test alongside [CheckSignalUniqueness].
func (c *Catalog) CheckCoverage(measurements ...health.Measurement) error {
	schemas := make([]measurementSchema, 0, len(measurements))
	for _, m := range measurements {
		keys := make(map[string]struct{})
		for _, k := range evaluation.SchemaKeys(m) {
			keys[k] = struct{}{}
		}
		schemas = append(schemas, measurementSchema{keys: keys, category: m.Category()})
	}

	c.mu.RLock()
	checks := make([]*health.CheckDefinition, 0, len(c.checks))
	for _, cd := range c.checks {
		checks = append(checks, cd)
	}
	c.mu.RUnlock()
	slices.SortFunc(checks, func(a, b *health.CheckDefinition) int { return cmp.Compare(a.ID, b.ID) })

	for _, cd := range checks {
		if !coveredBy(cd, schemas) {
			return fmt.Errorf(
				"check %q requires signals %v in category %s, but no supplied measurement type carries them",
				cd.ID, cd.RequiredSignals, cd.Category)
		}
	}
	return nil
}

// coveredBy reports whether some schema shares the check's category and
// carries all of its RequiredSignals — the schema-level form of the
// routing predicate the evaluation engine applies to a live measurement.
func coveredBy(cd *health.CheckDefinition, schemas []measurementSchema) bool {
	for _, s := range schemas {
		if s.category != cd.Category {
			continue
		}
		hasAll := true
		for _, sig := range cd.RequiredSignals {
			if _, ok := s.keys[sig]; !ok {
				hasAll = false
				break
			}
		}
		if hasAll {
			return true
		}
	}
	return false
}
