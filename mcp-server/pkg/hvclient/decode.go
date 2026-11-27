// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
)

// SchemaVersion is the verdict schema this server reads. serve documents other
// versions as unsupported.
const SchemaVersion = health.NodeVerdictSchemaVersion

// maxDepth bounds JSON nesting. A 0.7 verdict is about six levels deep.
const maxDepth = 64

var errNoSeverity = errors.New("the verdict has no overall severity")

// DecodeVerdict reads serve's JSON into the protobuf verdict. Object keys the 0.7
// schema does not define are dropped first, since a later serve may add some; then the
// rest is decoded strictly, so an enum name or number the schema does not define is
// refused at any depth. The verdict must also pass health-validation's own wire checks,
// name a severity on every check, and have an overall severity no lower than its worst
// check: a verdict that fails here is never read as a PASS.
func DecodeVerdict(raw []byte) (*healthv1.NodeVerdict, error) {
	verdict := &healthv1.NodeVerdict{}

	known, err := knownFieldsOnly(raw, verdict.ProtoReflect().Descriptor())
	if err != nil {
		return nil, &Error{Kind: KindBadVerdict, cause: err}
	}

	if err := protojson.Unmarshal(known, verdict); err != nil {
		return nil, &Error{Kind: KindBadVerdict, cause: err}
	}

	if err := checkEnums(verdict.ProtoReflect(), "verdict"); err != nil {
		return nil, &Error{Kind: KindBadVerdict, cause: err}
	}

	if verdict.GetSchemaVersion() != SchemaVersion {
		return nil, &Error{
			Kind:  KindUnsupportedSchema,
			cause: fmt.Errorf("schema version %q, want %q", verdict.GetSchemaVersion(), SchemaVersion),
		}
	}

	if verdict.OverallSeverity == nil || verdict.GetOverallSeverity() == healthv1.Severity_SEVERITY_UNSPECIFIED {
		return nil, &Error{Kind: KindBadVerdict, cause: errNoSeverity}
	}

	for name := range verdict.GetSeverityByDomain() {
		if _, ok := healthv1.Category_value[name]; !ok {
			return nil, &Error{Kind: KindBadVerdict, cause: fmt.Errorf("unknown domain %q", name)}
		}
	}

	if _, err := verdictproto.FromProto(verdict); err != nil {
		return nil, &Error{Kind: KindBadVerdict, cause: err}
	}

	if err := checkConsistent(verdict); err != nil {
		return nil, &Error{Kind: KindBadVerdict, cause: err}
	}

	return verdict, nil
}

// checkConsistent refuses fields the wire checks let through but the advice relies on.
func checkConsistent(verdict *healthv1.NodeVerdict) error {
	worst := healthv1.Severity_PASS

	for i, result := range verdict.GetCheckResults() {
		if result.GetSeverity() == healthv1.Severity_SEVERITY_UNSPECIFIED {
			return fmt.Errorf("check_results[%d] has no severity", i)
		}

		worst = max(worst, result.GetSeverity())
	}

	for i, skipped := range verdict.GetSkippedChecks() {
		if skipped.SkipReason == nil || skipped.GetSkipReason() == healthv1.SkipReason_NOT_SKIPPED {
			return fmt.Errorf("skipped_checks[%d] has no skip reason", i)
		}
	}

	// Severity numbers rise with severity, PASS to CRITICAL.
	if verdict.GetOverallSeverity() < worst {
		return fmt.Errorf("overall severity %s is lower than a check's %s", verdict.GetOverallSeverity(), worst)
	}

	if action := verdict.GetRecommendedAction(); action != nil &&
		action.GetOperationalState() == healthv1.OperationalState_OPERATIONAL_STATE_UNSPECIFIED {
		return errors.New("the recommended action has no operational state")
	}

	return nil
}

// knownFieldsOnly re-encodes raw without the object keys the message does not define.
// It refuses duplicate keys and trailing data, which protojson would also refuse.
func knownFieldsOnly(raw []byte, message protoreflect.MessageDescriptor) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	value, err := readJSON(decoder, 0)
	if err != nil {
		return nil, err
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the verdict")
	}

	prune(value, message)

	return json.Marshal(value)
}

func readJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nested deeper than %d", maxDepth)
	}

	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}

	switch delim {
	case '{':
		object := map[string]any{}

		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}

			name, _ := key.(string)
			if _, seen := object[name]; seen {
				return nil, fmt.Errorf("duplicate key %q", name)
			}

			if object[name], err = readJSON(decoder, depth+1); err != nil {
				return nil, err
			}
		}

		_, err = decoder.Token()

		return object, err
	case '[':
		list := []any{}

		for decoder.More() {
			item, err := readJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}

			list = append(list, item)
		}

		_, err = decoder.Token()

		return list, err
	default:
		return nil, fmt.Errorf("unexpected %q", delim)
	}
}

// prune deletes keys the message does not define and walks into nested messages.
// Well-known types such as google.protobuf.Value hold free JSON and are left alone.
func prune(value any, message protoreflect.MessageDescriptor) {
	object, ok := value.(map[string]any)
	if !ok || strings.HasPrefix(string(message.FullName()), "google.protobuf.") {
		return
	}

	fields := message.Fields()

	for key, item := range object {
		field := fields.ByJSONName(key)
		if field == nil {
			field = fields.ByTextName(key)
		}

		if field == nil {
			delete(object, key)
			continue
		}

		switch {
		case field.IsMap():
			if nested, ok := item.(map[string]any); ok && field.MapValue().Kind() == protoreflect.MessageKind {
				for _, entry := range nested {
					prune(entry, field.MapValue().Message())
				}
			}
		case field.Kind() != protoreflect.MessageKind:
		case field.IsList():
			if list, ok := item.([]any); ok {
				for _, entry := range list {
					prune(entry, field.Message())
				}
			}
		default:
			prune(item, field.Message())
		}
	}
}

// checkEnums walks a message and refuses enum numbers the schema does not name.
// Protobuf enums are open, so protojson accepts any number without this.
func checkEnums(message protoreflect.Message, path string) error {
	var err error

	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		err = checkField(field, value, path+"."+string(field.Name()))
		return err == nil
	})

	return err
}

func checkField(field protoreflect.FieldDescriptor, value protoreflect.Value, path string) error {
	switch {
	case field.IsList():
		list := value.List()
		for i := range list.Len() {
			if err := checkValue(field, list.Get(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case field.IsMap():
		var err error

		value.Map().Range(func(key protoreflect.MapKey, item protoreflect.Value) bool {
			err = checkValue(field.MapValue(), item, path+"["+key.String()+"]")
			return err == nil
		})

		return err
	default:
		return checkValue(field, value, path)
	}

	return nil
}

func checkValue(field protoreflect.FieldDescriptor, value protoreflect.Value, path string) error {
	switch field.Kind() {
	case protoreflect.EnumKind:
		if field.Enum().Values().ByNumber(value.Enum()) == nil {
			return fmt.Errorf("%s: enum value %d is not defined", path, value.Enum())
		}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return checkEnums(value.Message(), path)
	default:
	}

	return nil
}
