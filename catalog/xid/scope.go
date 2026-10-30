// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

// ScopeObservedValues implements [health.ObservedValuesScoper]: a check
// from [Checks] reports only its own events, grouped, and count entries.
func (XidMeasurement) ScopeObservedValues(checkID string, observed map[string]any) map[string]any {
	s, ok := checkScopes[checkID]
	if !ok {
		// shortcut: a custom Xid check is not in checkScopes, so it reports every code; add a way to declare its code if users ask.
		return observed
	}
	return s.apply(observed)
}

// checkScope is what one check from [Checks] judges: an Xid code, or an
// NVLink5 fault subtype such as "149.4" (see [XidEvent.SubXidCode]).
type checkScope struct {
	sub  string // set for a sub-code check
	code int64  // set for a code check
}

// checkScopes maps each check [Checks] builds to its scope, from the same
// tables and ID functions as the checks.
var checkScopes = func() map[string]checkScope {
	out := map[string]checkScope{}
	addCode := func(code int64) { out[codeCheckID(code)] = checkScope{code: code} }
	for _, c := range criticalXids {
		addCode(c.code)
	}
	for _, c := range nvlink5Xids {
		addCode(c.code)
	}
	for _, class := range warningClasses {
		for _, code := range class.codes {
			addCode(code)
		}
	}
	addCode(45)
	addCode(154)
	for _, c := range nvlink5SubXidCodes {
		out[subXidCodeCheckID(c.subXidCode)] = checkScope{sub: c.subXidCode}
	}
	return out
}()

// keep reports whether e is one of the check's own events. A sub-code
// check keeps the events sub_xid_code_counts counts under its subtype.
func (s checkScope) keep(e XidEvent) bool {
	if s.sub == "" {
		return e.XidCode == s.code
	}
	sub, ok := e.SubXidCode()
	return ok && sub == s.sub && hasSubXidCodeChecks(e.XidCode, e.NVLink5.SubComponent)
}

// apply returns a narrowed copy, because observed is shared with the rules.
func (s checkScope) apply(observed map[string]any) map[string]any {
	out := make(map[string]any, len(observed))
	for name, value := range observed {
		switch v := value.(type) {
		case []XidEvent:
			if name == SignalEvents {
				value = groupEvents(v, s.keep)
			}
		case map[int64]int64:
			if s.sub == "" && (name == SignalXidCounts || name == SignalFatalXidCounts) {
				value = onlyKey(v, s.code)
			}
		case map[string]int64:
			if s.sub != "" && name == SignalSubXidCodeCounts {
				value = onlyKey(v, s.sub)
			}
		}
		out[name] = value
	}
	return out
}

// groupEvents merges events with the same message, since every field is
// parsed from the line, and counts them in Count.
func groupEvents(events []XidEvent, keep func(XidEvent) bool) []XidEvent {
	var out []XidEvent
	index := map[string]int{} // message -> its event in out
	for _, e := range events {
		if !keep(e) {
			continue
		}
		if i, ok := index[e.Message]; ok {
			out[i].Count++
			continue
		}
		e.Count = 1
		// shortcut: events without a message are never merged; group by the other fields if outside producers omit it.
		if e.Message != "" {
			index[e.Message] = len(out)
		}
		out = append(out, e)
	}
	return out
}

// onlyKey returns nil when counts has no key, as a measurement with no events does.
func onlyKey[K comparable](counts map[K]int64, key K) map[K]int64 {
	n, ok := counts[key]
	if !ok {
		return nil
	}
	return map[K]int64{key: n}
}
