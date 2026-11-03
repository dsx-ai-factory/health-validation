// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package verdictproto_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
)

func fixtures() map[string]health.NodeVerdict {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 123, time.FixedZone("offset", -8*60*60))
	pass := health.CheckVerdict{Timestamp: stamp, CheckID: "gpu_power", Category: health.GPU, Severity: health.Pass, NVIDIAEndorsed: true}
	critical := pass
	critical.Severity = health.Critical
	critical.ConditionScope = health.DomainScope
	critical.RecommendedAction = health.Drain(health.RemediationActionResetGPU, "custom_step")
	critical.Messages = []string{"power outside range"}
	critical.AffectedEntities = []string{"GPU-1"}
	critical.ObservedValues = map[string]any{"watts": int64(300), "nested": map[string]any{"null": nil, "list": []any{"text", true, 1.25, []string{"a", "b"}}}}
	critical.Thresholds = map[string]health.ResolvedThreshold{"max": {Value: int64(250), OriginalValue: nil, Category: health.AdjustableThreshold, Overridden: true}}
	skipped := pass
	skipped.Skipped = true
	skipped.SkipReason = health.CadenceMismatch
	skipped.SkipDetail = "outside requested cadence"
	discovery := health.CheckVerdict{Timestamp: stamp, CheckID: "architecture_discovery", Category: health.Node, Severity: health.Critical, RecommendedAction: health.Drain(health.RemediationActionCheckCollection), Messages: []string{"discovery unavailable"}}
	emptyCheckIDs := health.AggregateNodeVerdict(nil)
	emptyCheckIDs.AffectedComponents = []health.AffectedComponent{{CheckIDs: []string{}, Category: health.GPU, MaxSeverity: health.Warning}}
	return map[string]health.NodeVerdict{
		"empty":           health.AggregateNodeVerdict(nil),
		"pass":            health.AggregateNodeVerdict([]health.CheckVerdict{pass}),
		"critical":        health.AggregateNodeVerdict([]health.CheckVerdict{critical}),
		"skipped":         health.AggregateNodeVerdict([]health.CheckVerdict{skipped}),
		"discovery":       health.AggregateNodeVerdict([]health.CheckVerdict{discovery}),
		"empty_check_ids": emptyCheckIDs,
	}
}

func TestRoundTripAndSchema(t *testing.T) {
	schema := readSchema(t)
	for name, original := range fixtures() {
		t.Run(name, func(t *testing.T) {
			wire, err := verdictproto.ToProto(original)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := verdictproto.MarshalJSON(wire)
			if err != nil {
				t.Fatal(err)
			}
			validateSchema(t, schema, schema, decode(t, actual), "verdict")
			expected, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decode(t, expected), decode(t, actual)) {
				t.Fatalf("wire JSON differs from catalog JSON\nwant %s\ngot  %s", expected, actual)
			}
			back, err := verdictproto.FromProto(wire)
			if err != nil {
				t.Fatal(err)
			}
			backJSON, err := json.Marshal(back)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decode(t, expected), decode(t, backJSON)) {
				t.Fatalf("round trip changed JSON\nwant %s\ngot  %s", expected, backJSON)
			}
		})
	}
}

func TestRejectsLossyOrUnsupportedValues(t *testing.T) {
	for _, value := range []any{int64(1<<53 + 1), uint64(math.MaxUint64), json.Number("1.0000000000000000001"), math.NaN(), math.Inf(1), make(chan int)} {
		v := fixtures()["pass"]
		v.CheckResults[0].ObservedValues = map[string]any{"nested": []any{value}}
		if _, err := verdictproto.ToProto(v); err == nil {
			t.Errorf("accepted unsupported value %#v", value)
		}
	}
	v := fixtures()["pass"]
	v.CheckResults[0].ObservedValues = map[string]any{"exact_integer": int64(1 << 53)}
	if _, err := verdictproto.ToProto(v); err != nil {
		t.Fatalf("rejected exact integer boundary: %v", err)
	}
}

func TestDecimalPrecisionAndEquivalentScientificNotation(t *testing.T) {
	for _, number := range []json.Number{"1.0000000000000000001", "0.123456789012345678901", "1e-400"} {
		v := fixtures()["pass"]
		v.CheckResults[0].Thresholds = map[string]health.ResolvedThreshold{
			"nested": {Value: map[string]any{"values": []any{number}}, OriginalValue: nil},
		}
		if _, err := verdictproto.ToProto(v); err == nil {
			t.Errorf("accepted lossy nested threshold %s", number)
		}
	}
	for _, number := range []any{0.1, 1.25, int64(1 << 53), float64(1e20), math.MaxFloat64, json.Number("1e3"), json.Number("0.10"), json.Number("1.25e-2")} {
		v := fixtures()["pass"]
		v.CheckResults[0].ObservedValues = map[string]any{"number": number}
		wire, err := verdictproto.ToProto(v)
		if err != nil {
			t.Fatalf("rejected representable number %v: %v", number, err)
		}
		back, err := verdictproto.FromProto(wire)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(number)
		if err != nil {
			t.Fatal(err)
		}
		before, ok := new(big.Rat).SetString(string(encoded))
		if !ok {
			t.Fatalf("invalid original number %s", encoded)
		}
		encoded, err = json.Marshal(back.CheckResults[0].ObservedValues["number"])
		if err != nil {
			t.Fatal(err)
		}
		after, ok := new(big.Rat).SetString(string(encoded))
		if !ok || before.Cmp(after) != 0 {
			t.Errorf("numeric meaning changed for %v: got %s", number, encoded)
		}
	}
}

func TestLargePowerTolerancePreservesCatalogVerdict(t *testing.T) {
	const tolerance = 1e100
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
			CheckID: "gpu_average_power_draw", ThresholdName: "max_power_w", Value: float64(tolerance),
		})),
	)
	check, err := cat.Evaluate("gpu_average_power_draw", gpu.GPUMeasurement{GPUID: "GPU-0", AveragePowerDrawW: 500})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := verdictproto.ToProto(health.AggregateNodeVerdict([]health.CheckVerdict{check}))
	if err != nil {
		t.Fatalf("valid large power tolerance rejected: %v", err)
	}
	back, err := verdictproto.FromProto(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.CheckResults[0].Thresholds["max_power_w"].Value; got != float64(tolerance) {
		t.Fatalf("large tolerance changed: got %v, want %g", got, float64(tolerance))
	}
}

func TestRejectsUnknownEnumsAndIncompleteVerdicts(t *testing.T) {
	v := fixtures()["pass"]
	v.OverallSeverity = health.Severity(100)
	if _, err := verdictproto.ToProto(v); err == nil {
		t.Fatal("accepted unknown catalog severity")
	}
	wire, err := verdictproto.ToProto(fixtures()["pass"])
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*healthv1.NodeVerdict){
		func(v *healthv1.NodeVerdict) { v.OverallSeverity = healthv1.Severity(100).Enum() },
		func(v *healthv1.NodeVerdict) { v.SchemaVersion = proto.String("99.0") },
		func(v *healthv1.NodeVerdict) { v.TraceId = nil },
		func(v *healthv1.NodeVerdict) { v.CheckResults[0].Skipped = nil },
	} {
		bad := proto.Clone(wire).(*healthv1.NodeVerdict)
		mutate(bad)
		if _, err := verdictproto.FromProto(bad); err == nil {
			t.Fatal("accepted malformed wire verdict")
		}
	}
	if _, err := verdictproto.FromProto(nil); err == nil {
		t.Fatal("accepted nil verdict")
	}
}

func TestSchemaFieldsAndEnumsHaveProtoCounterparts(t *testing.T) {
	schema := readSchema(t)
	defs := schema["$defs"].(map[string]any)
	messages := map[string]protoreflect.MessageDescriptor{
		"":                  (&healthv1.NodeVerdict{}).ProtoReflect().Descriptor(),
		"checkVerdict":      (&healthv1.CheckVerdict{}).ProtoReflect().Descriptor(),
		"resolvedThreshold": (&healthv1.ResolvedThreshold{}).ProtoReflect().Descriptor(),
		"recommendedAction": (&healthv1.RecommendedAction{}).ProtoReflect().Descriptor(),
		"affectedComponent": (&healthv1.AffectedComponent{}).ProtoReflect().Descriptor(),
	}
	for name, message := range messages {
		definition := schema
		if name != "" {
			definition = defs[name].(map[string]any)
		}
		properties := definition["properties"].(map[string]any)
		if message.Fields().Len() != len(properties) {
			t.Errorf("%s has %d proto fields vs %d schema properties", name, message.Fields().Len(), len(properties))
		}
		for field := range properties {
			if message.Fields().ByName(protoreflect.Name(field)) == nil {
				t.Errorf("%s.%s has no proto counterpart", name, field)
			}
		}
	}
	enums := map[string]protoreflect.EnumDescriptor{
		"severity":          healthv1.Severity_PASS.Descriptor(),
		"category":          healthv1.Category_gpu.Descriptor(),
		"conditionScope":    healthv1.ConditionScope_NODE.Descriptor(),
		"skipReason":        healthv1.SkipReason_NOT_SKIPPED.Descriptor(),
		"maturityLevel":     healthv1.MaturityLevel_stable.Descriptor(),
		"thresholdCategory": healthv1.ThresholdCategory_DEFAULT.Descriptor(),
		"operationalState":  healthv1.OperationalState_ready.Descriptor(),
	}
	for name, enum := range enums {
		names := defs[name].(map[string]any)["enum"].([]any)
		if len(names) != enum.Values().Len() {
			t.Errorf("%s enum count drift", name)
		}
		for _, value := range names {
			if enum.Values().ByName(protoreflect.Name(value.(string))) == nil {
				t.Errorf("%s value %s has no proto mapping", name, value)
			}
		}
	}
}

func TestEveryCatalogEnumMapsByWireName(t *testing.T) {
	enums := []struct {
		catalog func(int) any
		wire    protoreflect.EnumDescriptor
	}{
		{func(i int) any { return health.Severity(i) }, healthv1.Severity_PASS.Descriptor()},
		{func(i int) any { return health.Category(i) }, healthv1.Category_gpu.Descriptor()},
		{func(i int) any { return health.ConditionScope(i) }, healthv1.ConditionScope_NODE.Descriptor()},
		{func(i int) any { return health.SkipReason(i) }, healthv1.SkipReason_NOT_SKIPPED.Descriptor()},
		{func(i int) any { return health.MaturityLevel(i) }, healthv1.MaturityLevel_stable.Descriptor()},
		{func(i int) any { return health.ThresholdCategory(i) }, healthv1.ThresholdCategory_DEFAULT.Descriptor()},
		{func(i int) any { return health.OperationalState(i) }, healthv1.OperationalState_ready.Descriptor()},
		{func(i int) any { return health.SchedulingCadence(i) }, healthv1.SchedulingCadence_CONTINUOUS.Descriptor()},
		{func(i int) any { return health.ExecutionContext(i) }, healthv1.ExecutionContext_OPERATOR_INFRA.Descriptor()},
	}
	for _, enum := range enums {
		count := 0
		for i := 0; i < 256; i++ {
			encoded, err := json.Marshal(enum.catalog(i))
			if err != nil {
				continue
			}
			var name string
			if err := json.Unmarshal(encoded, &name); err != nil {
				t.Fatal(err)
			}
			count++
			if enum.wire.Values().ByName(protoreflect.Name(name)) == nil {
				t.Errorf("%s has no wire mapping for %s", enum.wire.FullName(), name)
			}
		}
		if count != enum.wire.Values().Len() {
			t.Errorf("%s count mismatch: catalog %d wire %d", enum.wire.FullName(), count, enum.wire.Values().Len())
		}
	}
}

func FuzzDynamicValuesRoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("nested values\x00\xff"))
	f.Add(bytes.Repeat([]byte{0xff}, 16))
	f.Add([]byte("Unicode: \xe2\x98\x83"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip()
		}
		var bits [8]byte
		copy(bits[:], data)
		integer := int64(binary.LittleEndian.Uint64(bits[:]) & ((1 << 53) - 1))
		if bits[0]&1 != 0 {
			integer = -integer
		}
		fraction := float64(int32(binary.LittleEndian.Uint32(bits[:4]))) / 256
		verdict := health.AggregateNodeVerdict([]health.CheckVerdict{{
			Timestamp: time.Unix(1, 123).UTC(),
			CheckID:   "dynamic_values",
			Category:  health.GPU,
			ObservedValues: map[string]any{
				"nested": []any{nil, len(data)%2 == 0, string(data), map[string]any{
					"integer": integer, "fraction": fraction, "empty": []any{},
				}},
			},
			Thresholds: map[string]health.ResolvedThreshold{"dynamic": {
				Value: integer, OriginalValue: fraction, Category: health.DefaultThreshold,
			}},
		}})
		wire, err := verdictproto.ToProto(verdict)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := verdictproto.FromProto(wire)
		if err != nil {
			t.Fatal(err)
		}
		before, err := json.Marshal(verdict)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(roundTrip)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decode(t, before), decode(t, after)) {
			t.Fatalf("dynamic values changed\nwant %s\ngot  %s", before, after)
		}
	})
}

func readSchema(t *testing.T) map[string]any {
	t.Helper()
	paths := []string{filepath.Join("..", "..", "catalog", "health", "node-verdict.schema.json")}
	if root := os.Getenv("TEST_SRCDIR"); root != "" {
		paths = append(paths, filepath.Join(root, os.Getenv("TEST_WORKSPACE"), "catalog", "health", "node-verdict.schema.json"))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			return decode(t, data).(map[string]any)
		}
	}
	t.Fatal("catalog verdict JSON schema not found in test data")
	return nil
}

func decode(t *testing.T, data []byte) any {
	t.Helper()
	var out any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Exercise the contract's shape, enum, presence and scalar constraints without
// introducing a schema library into the shipped operator's module graph.
func validateSchema(t *testing.T, root, schema map[string]any, value any, path string) {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		validateSchema(t, root, root["$defs"].(map[string]any)[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any), value, path)
		return
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		t.Errorf("%s = %v, want constant %v", path, value, constant)
	}
	if values, ok := schema["enum"].([]any); ok && !slices.Contains(values, value) {
		t.Errorf("%s has unknown enum value %v", path, value)
	}
	kind := "null"
	switch value.(type) {
	case map[string]any:
		kind = "object"
	case []any:
		kind = "array"
	case string:
		kind = "string"
	case bool:
		kind = "boolean"
	case json.Number:
		kind = "number"
	}
	if expected, ok := schema["type"]; ok {
		allowed := []any{expected}
		if union, ok := expected.([]any); ok {
			allowed = union
		}
		if !slices.Contains(allowed, any(kind)) {
			t.Fatalf("%s has type %s, want %v", path, kind, expected)
		}
	}
	switch v := value.(type) {
	case map[string]any:
		for _, required := range asList(schema["required"]) {
			if _, ok := v[required.(string)]; !ok {
				t.Errorf("%s missing required %s", path, required)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range v {
			if names, ok := schema["propertyNames"].(map[string]any); ok {
				validateSchema(t, root, names, key, path+".<key>")
			}
			definition, ok := properties[key].(map[string]any)
			if !ok {
				definition, ok = schema["additionalProperties"].(map[string]any)
			}
			if ok {
				validateSchema(t, root, definition, child, path+"."+key)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for _, item := range v {
				validateSchema(t, root, items, item, path+"[]")
			}
		}
	case string:
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				t.Errorf("%s invalid timestamp: %v", path, err)
			}
		}
		if min, ok := schema["minLength"].(json.Number); ok && min == "1" && v == "" {
			t.Errorf("%s must not be empty", path)
		}
	}
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}
