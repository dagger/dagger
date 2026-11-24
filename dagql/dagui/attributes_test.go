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

	testSpanAttributesRoundTrip(t, snapshot, want)
	EncodeBinaryAttributes(true)
	testSpanAttributesRoundTrip(t, snapshot, want)
	EncodeBinaryAttributes(false)

	// no attributes: omitted from JSON like an empty map
	empty, err := json.Marshal(SpanSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(empty, []byte("ExtraAttributes")) {
		t.Fatalf("empty attributes should be omitted: %s", empty)
	}
}

// TestSpanAttributesSharePairs checks that spans share a low-cardinality
// attribute's name and value, and that updating one span's never touches
// another's.
func TestSpanAttributesSharePairs(t *testing.T) {
	db := NewDB()
	var a, b SpanSnapshot
	a.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, string([]byte("hit")))
	b.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, string([]byte("hit")))
	a.ProcessAttribute("z.str", "x")
	b.ProcessAttribute("z.str", "x")
	db.internSnapshotStrings(&a)
	db.internSnapshotStrings(&b)
	pair := func(s SpanSnapshot, name string) *spanAttribute {
		return s.ExtraAttributes[s.ExtraAttributes.find(name)]
	}
	if pair(a, telemetryattrs.CacheOutcomeAttr) != pair(b, telemetryattrs.CacheOutcomeAttr) {
		t.Fatal("equal low-cardinality attributes should share one spanAttribute")
	}
	if pair(a, "z.str") == pair(b, "z.str") {
		t.Fatal("ordinary attributes should not be shared")
	}
	a.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, "miss")
	db.internSnapshotStrings(&a)
	if got, _ := a.ExtraAttributes.String(telemetryattrs.CacheOutcomeAttr); got != "miss" {
		t.Fatalf("updated span reads %q", got)
	}
	if got, _ := b.ExtraAttributes.String(telemetryattrs.CacheOutcomeAttr); got != "hit" {
		t.Fatalf("other span reads %q after an update elsewhere", got)
	}
	// Another DB shares nothing with this one.
	other := NewDB()
	var c SpanSnapshot
	c.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, "hit")
	other.internSnapshotStrings(&c)
	if pair(c, telemetryattrs.CacheOutcomeAttr) == pair(b, telemetryattrs.CacheOutcomeAttr) {
		t.Fatal("DBs should not share attributes")
	}
}

// TestSpanAttributesGobForms checks the two forms SpanAttributes gob-encode
// as: JSON by default, which every decoder reads, and the binary form
// EncodeBinaryAttributes opts in to, which only decoders that know it do.
func TestSpanAttributesGobForms(t *testing.T) {
	var snapshot SpanSnapshot
	snapshot.ProcessAttribute("z.str", "goodbye <world>")
	snapshot.ProcessAttribute("a.bool", true)
	snapshot.ProcessAttribute("m.nul", "\x00not json")
	snapshot.ProcessAttribute("m.utf8", "héllo\u2028\n")
	attrs := snapshot.ExtraAttributes

	same := func(t *testing.T, got SpanAttributes) {
		t.Helper()
		if len(got) != len(attrs) {
			t.Fatalf("decoded %d attributes, want %d", len(got), len(attrs))
		}
		for name, payload := range attrs.All() {
			if p, ok := got.Get(name); !ok || !bytes.Equal(p, payload) {
				t.Errorf("Get(%q) = %s, %v; want %s", name, p, ok, payload)
			}
		}
		if str, ok := got.String("m.nul"); !ok || str != "\x00not json" {
			t.Errorf("String(m.nul) = %q, %v", str, ok)
		}
	}

	jsonForm, err := attrs.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := attrs.MarshalJSON(); !bytes.Equal(jsonForm, want) {
		t.Fatalf("default gob form is %q, want the JSON %q", jsonForm, want)
	}

	EncodeBinaryAttributes(true)
	defer EncodeBinaryAttributes(false)
	binForm, err := attrs.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	if binForm[0] != binaryAttrsVersion {
		t.Fatalf("binary form starts with %q", binForm[0])
	}
	if nilForm, _ := SpanAttributes(nil).GobEncode(); string(nilForm) != "null" {
		t.Fatalf("nil attributes encode as %q", nilForm)
	}

	t.Run("decoder reads both forms", func(t *testing.T) {
		for _, form := range [][]byte{jsonForm, binForm} {
			var got SpanAttributes
			if err := got.GobDecode(form); err != nil {
				t.Fatal(err)
			}
			same(t, got)
		}
		var got SpanAttributes
		if err := got.GobDecode([]byte("null")); err != nil || got != nil {
			t.Fatalf("null decodes to %v, %v", got, err)
		}
		empty, _ := SpanAttributes{}.GobEncode()
		if err := got.GobDecode(empty); err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty attributes decode to %#v, %v", got, err)
		}
	})

	t.Run("old decoder fails on binary form", func(t *testing.T) {
		// what a decoder from before the binary form does with it: why
		// it's opt-in
		var got SpanAttributes
		if err := got.UnmarshalJSON(binForm); err == nil {
			t.Fatal("decoding the binary form as JSON should fail")
		}
	})

	t.Run("malformed binary form", func(t *testing.T) {
		for i := 1; i < len(binForm); i++ {
			var got SpanAttributes
			if err := got.GobDecode(binForm[:i]); err == nil {
				t.Fatalf("decoding %d of %d bytes should fail", i, len(binForm))
			}
		}
		var got SpanAttributes
		if err := got.GobDecode(append(slices.Clone(binForm), 0)); err == nil {
			t.Fatal("decoding trailing bytes should fail")
		}
		if err := got.GobDecode([]byte{binaryAttrsVersion, 0xff, 0xff, 0xff, 0xff, 0x0f}); err == nil {
			t.Fatal("decoding a huge count should fail")
		}
	})
}

// TestAppendJSONString checks that attribute strings marshal as
// encoding/json marshals them.
func TestAppendJSONString(t *testing.T) {
	for _, s := range []string{
		"", "plain", "with space", "quote\"", `back\slash`, "<html>&", "tab\t",
		"nul\x00", "héllo", "\u2028\u2029", "invalid \xff utf8", "\x7f",
	} {
		want, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := appendJSONString([]byte("x"), s); string(got) != "x"+string(want) {
			t.Errorf("appendJSONString(%q) = %s, want x%s", s, got, want)
		}
	}
}

// testSpanAttributesRoundTrip checks that a snapshot's attributes survive
// both JSON and gob round trips.
func testSpanAttributesRoundTrip(t *testing.T, snapshot SpanSnapshot, want map[string]json.RawMessage) {
	t.Helper()
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
}
