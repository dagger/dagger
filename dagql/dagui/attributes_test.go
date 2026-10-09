package dagui

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

func TestSpanAttributes(t *testing.T) {
	var snapshot SpanSnapshot
	snapshot.ProcessAttribute("z.str", "hello <world>")
	snapshot.ProcessAttribute("a.bool", true)
	snapshot.ProcessAttribute("m.int", int64(42))
	snapshot.ProcessAttribute("m.slice", []string{"x", "y"})
	snapshot.ProcessAttribute("m.nul", "\x00not json")
	snapshot.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, "hit")
	// a later export of the span replaces the value
	snapshot.ProcessAttribute("z.str", "goodbye <world>")

	// the same content a map[string]json.RawMessage of the values held
	want := map[string]json.RawMessage{}
	for name, val := range map[string]any{
		"z.str":                         "goodbye <world>",
		"a.bool":                        true,
		"m.int":                         int64(42),
		"m.slice":                       []string{"x", "y"},
		"m.nul":                         "\x00not json",
		telemetryattrs.CacheOutcomeAttr: "hit",
	} {
		payload, err := json.Marshal(val)
		if err != nil {
			t.Fatal(err)
		}
		want[name] = payload
	}

	attrs := snapshot.ExtraAttributes
	if len(attrs) != len(want) {
		t.Fatalf("got %d attributes, want %d", len(attrs), len(want))
	}
	for name, payload := range want {
		got, ok := attrs.Get(name)
		if !ok || !bytes.Equal(got, payload) {
			t.Errorf("Get(%q) = %s, %v; want %s", name, got, ok, payload)
		}
	}
	if _, ok := attrs.Get("missing"); ok {
		t.Error("Get of a missing attribute should fail")
	}
	if str, ok := attrs.String("z.str"); !ok || str != "goodbye <world>" {
		t.Errorf("String(z.str) = %q, %v", str, ok)
	}
	if str, ok := attrs.String("m.nul"); !ok || str != "\x00not json" {
		t.Errorf("String(m.nul) = %q, %v", str, ok)
	}
	if _, ok := attrs.String("a.bool"); ok {
		t.Error("String of a bool attribute should fail")
	}
	if names := slices.Collect(maps.Keys(maps.Collect(attrs.All()))); len(names) != len(want) {
		t.Errorf("All yielded %v", names)
	}
	var allNames []string
	for name := range attrs.All() {
		allNames = append(allNames, name)
	}
	if !slices.IsSorted(allNames) {
		t.Errorf("All should yield names in order, got %v", allNames)
	}

	// JSON is byte-for-byte what the map marshaled to
	gotJSON, err := json.Marshal(attrs)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("JSON = %s, want %s", gotJSON, wantJSON)
	}

	// and both JSON and gob round-trip through a snapshot
	snapJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON SpanSnapshot
	if err := json.Unmarshal(snapJSON, &fromJSON); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(snapshot); err != nil {
		t.Fatal(err)
	}
	var fromGob SpanSnapshot
	if err := gob.NewDecoder(&buf).Decode(&fromGob); err != nil {
		t.Fatal(err)
	}
	for _, decoded := range []SpanAttributes{fromJSON.ExtraAttributes, fromGob.ExtraAttributes} {
		for name, payload := range want {
			got, ok := decoded.Get(name)
			if !ok || !bytes.Equal(got, payload) {
				t.Errorf("decoded Get(%q) = %s, %v; want %s", name, got, ok, payload)
			}
		}
		if str, ok := decoded.String("z.str"); !ok || str != "goodbye <world>" {
			t.Errorf("decoded String(z.str) = %q, %v", str, ok)
		}
	}

	// no attributes: omitted from JSON like an empty map
	empty, err := json.Marshal(SpanSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(empty, []byte("ExtraAttributes")) {
		t.Fatalf("empty attributes should be omitted: %s", empty)
	}
}
