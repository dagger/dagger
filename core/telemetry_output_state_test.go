package core

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
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
	if _, ok := payload.Fields["SkippedVal"]; ok {
		t.Fatalf("skipped json field should not be present")
	}

	refField, ok := payload.Fields["ref"]
	if !ok {
		t.Fatalf("missing ref field: %#v", payload.Fields)
	}
	if got, ok := refField.Value.(string); !ok || got != refID.Digest().String() {
		t.Fatalf("unexpected ref field value: %#v", refField.Value)
	}

	rawField, ok := payload.Fields["raw"]
	if !ok {
		t.Fatalf("missing raw field")
	}
	wantRaw := base64.StdEncoding.EncodeToString([]byte("abc"))
	if got, ok := rawField.Value.(string); !ok || got != wantRaw {
		t.Fatalf("unexpected raw field value: %#v", rawField.Value)
	}

	nestedField, ok := payload.Fields["nested"]
	if !ok {
		t.Fatalf("missing nested field")
	}
	nestedMap, ok := nestedField.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %#v", nestedField.Value)
	}
	if nestedMap["enabled"] != true || nestedMap["label"] != "nested" {
		t.Fatalf("unexpected nested payload: %#v", nestedMap)
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
	got, err := toOutputStateValue(reflect.ValueOf(nilRef), 0, map[visitKey]struct{}{})
	if err != nil {
		t.Fatalf("toOutputStateValue returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil value, got %#v", got)
	}
}

func TestToOutputStateValuePanickingIDMethod(t *testing.T) {
	t.Parallel()

	got, err := toOutputStateValue(reflect.ValueOf(outputStatePanicMethodIDable{}), 0, map[visitKey]struct{}{})
	if err != nil {
		t.Fatalf("toOutputStateValue returned error: %v", err)
	}
	asMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected struct to serialize as map, got %#v", got)
	}
	if len(asMap) != 0 {
		t.Fatalf("expected empty struct map, got %#v", asMap)
	}
}
