package core

import (
	"encoding/base64"
	"reflect"
	"slices"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

type outputStateTestRef struct {
	id *call.ID
}

func (r outputStateTestRef) ID() *call.ID {
	return r.id
}

type outputStateNested struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
}

type outputStateTestObject struct {
	Name       string             `json:"name"`
	Ref        outputStateTestRef `json:"ref"`
	Numbers    []int              `json:"numbers"`
	Raw        []byte             `json:"raw"`
	Nested     outputStateNested  `json:"nested"`
	SkippedVal string             `json:"-"`
}

type outputStatePanickyIDable struct {
	id *call.ID
}

func (r *outputStatePanickyIDable) ID() *call.ID {
	return r.id
}

type outputStatePanicMethodIDable struct{}

func (outputStatePanicMethodIDable) ID() *call.ID {
	panic("boom")
}

type outputStateTypedNilReceiver struct {
	name string
}

func (t *outputStateTypedNilReceiver) Type() *ast.Type {
	// Accessing a field forces panic when called on nil receiver.
	return &ast.Type{NamedType: t.name}
}

type outputStateTypedPanicky struct{}

func (outputStateTypedPanicky) Type() *ast.Type {
	panic("boom")
}

type outputStateTypeNameEdgeCaseObject struct {
	NilTyped   *outputStateTypedNilReceiver `json:"nilTyped"`
	PanicTyped outputStateTypedPanicky      `json:"panicTyped"`
}

func (*outputStateTypeNameEdgeCaseObject) Type() *ast.Type {
	return &ast.Type{NamedType: "OutputStateTypeNameEdgeCaseObject", NonNull: true}
}

func (*outputStateTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: "OutputStateTest", NonNull: true}
}

func TestBuildOutputStatePayloadFromTyped(t *testing.T) {
	t.Parallel()

	refID := call.New().
		Append(&ast.Type{NamedType: "Secret", NonNull: true}, "secret").
		WithDigest(digest.FromString("telemetry-output-state"))

	obj := &outputStateTestObject{
		Name:    "example",
		Ref:     outputStateTestRef{id: refID},
		Numbers: []int{1, 2, 3},
		Raw:     []byte("abc"),
		Nested: outputStateNested{
			Enabled: true,
			Label:   "nested",
		},
		SkippedVal: "redacted",
	}

	payload, err := buildOutputStatePayloadFromTyped(obj, obj.Type())
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if payload.Type != "OutputStateTest" {
		t.Fatalf("unexpected payload type: %q", payload.Type)
	}
	fields := outputStateFieldsByName(payload.Fields)
	if _, ok := fields["SkippedVal"]; ok {
		t.Fatalf("skipped json field should not be present")
	}

	refField, ok := fields["ref"]
	if !ok {
		t.Fatalf("missing ref field: %#v", payload.Fields)
	}
	if got := refField.GetValue().GetCallDigest(); got != refID.Digest().String() {
		t.Fatalf("unexpected ref field value: %#v", refField.GetValue())
	}
	if !slices.Equal(refField.GetRefs(), []string{refID.Digest().String()}) {
		t.Fatalf("unexpected ref field refs: %#v", refField.GetRefs())
	}

	rawField, ok := fields["raw"]
	if !ok {
		t.Fatalf("missing raw field")
	}
	wantRaw := base64.StdEncoding.EncodeToString([]byte("abc"))
	if got := rawField.GetValue().GetString_(); got != wantRaw {
		t.Fatalf("unexpected raw field value: %#v", rawField.GetValue())
	}

	nestedField, ok := fields["nested"]
	if !ok {
		t.Fatalf("missing nested field")
	}
	nestedObj := nestedField.GetValue().GetObject()
	if nestedObj == nil {
		t.Fatalf("expected nested object value, got %#v", nestedField.GetValue())
	}
	nestedVals := map[string]*callpbv1.Literal{}
	for _, arg := range nestedObj.GetValues() {
		nestedVals[arg.GetName()] = arg.GetValue()
	}
	if nestedVals["enabled"].GetBool() != true || nestedVals["label"].GetString_() != "nested" {
		t.Fatalf("unexpected nested payload: %#v", nestedObj)
	}
}

func TestOutputStateEmitterReserveAndRelease(t *testing.T) {
	t.Parallel()

	cache := newOutputStateEmitter(2)
	if !cache.TryReserve("trace-1", "output-1") {
		t.Fatalf("expected first reserve to succeed")
	}
	if cache.TryReserve("trace-1", "output-1") {
		t.Fatalf("expected duplicate reserve to fail")
	}

	cache.Release("trace-1", "output-1")
	if !cache.TryReserve("trace-1", "output-1") {
		t.Fatalf("expected reserve to succeed after release")
	}

	if !cache.TryReserve("trace-2", "output-1") {
		t.Fatalf("expected second trace reserve")
	}
	if !cache.TryReserve("trace-3", "output-1") {
		t.Fatalf("expected third trace reserve")
	}
	if len(cache.traces) > 2 {
		t.Fatalf("expected eviction to cap trace cache, got %d", len(cache.traces))
	}
}

func TestToOutputStateValueNilIDablePointer(t *testing.T) {
	t.Parallel()

	var nilRef *outputStatePanickyIDable
	got, refs, err := toOutputStateLiteral(reflect.ValueOf(nilRef), 0, map[visitKey]struct{}{})
	if err != nil {
		t.Fatalf("toOutputStateLiteral returned error: %v", err)
	}
	if got.GetNull() != true || len(refs) != 0 {
		t.Fatalf("expected null literal with no refs, got %#v refs=%#v", got, refs)
	}
}

func TestToOutputStateValuePanickingIDMethod(t *testing.T) {
	t.Parallel()

	got, refs, err := toOutputStateLiteral(reflect.ValueOf(outputStatePanicMethodIDable{}), 0, map[visitKey]struct{}{})
	if err != nil {
		t.Fatalf("toOutputStateLiteral returned error: %v", err)
	}
	obj := got.GetObject()
	if obj == nil {
		t.Fatalf("expected struct to serialize as object, got %#v", got)
	}
	if len(obj.GetValues()) != 0 || len(refs) != 0 {
		t.Fatalf("expected empty struct object, got %#v refs=%#v", obj, refs)
	}
}

func TestBuildOutputStatePayloadFromTypedTypeNameEdgeCases(t *testing.T) {
	t.Parallel()

	obj := &outputStateTypeNameEdgeCaseObject{}
	payload, err := buildOutputStatePayloadFromTyped(obj, obj.Type())
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	fields := outputStateFieldsByName(payload.Fields)
	nilTyped, ok := fields["nilTyped"]
	if !ok {
		t.Fatalf("missing nilTyped field")
	}
	if nilTyped.Type == "" {
		t.Fatalf("expected non-empty type fallback for nilTyped field")
	}

	panicTyped, ok := fields["panicTyped"]
	if !ok {
		t.Fatalf("missing panicTyped field")
	}
	if panicTyped.Type == "" {
		t.Fatalf("expected non-empty type fallback for panicTyped field")
	}
}

func outputStateFieldsByName(fields []*callpbv1.OutputStateField) map[string]*callpbv1.OutputStateField {
	out := make(map[string]*callpbv1.OutputStateField, len(fields))
	for _, field := range fields {
		if field == nil {
			continue
		}
		out[field.GetName()] = field
	}
	return out
}
