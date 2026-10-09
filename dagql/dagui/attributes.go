package dagui

import (
	"bytes"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// SpanAttributes holds a span's attributes that have no dedicated
// SpanSnapshot field, by name: the raw material for anything that wants to
// show or inspect them.
//
// It is a compact stand-in for a map[string]json.RawMessage. Spans carry a
// few such attributes each (cache evidence, wcprof bookkeeping, the origin
// client), and with millions of spans a map per span plus a JSON copy of
// every value dominated the heap. Instead the attributes sit in a short
// slice, names are interned, and string values -- nearly all of them -- are
// kept as they arrived, JSON-encoded only when read through Get, All or
// MarshalJSON. Other values are kept as their JSON encoding.
//
// It marshals to and from the same JSON object (and gob) a map would.
type SpanAttributes []spanAttribute

type spanAttribute struct {
	name string
	// val is the attribute's value: the string itself, or for any other
	// value its JSON encoding behind a jsonValueMarker.
	val string
}

// jsonValueMarker prefixes a spanAttribute value held as JSON. A string
// value starting with it is held as JSON too, so the two never mix up.
const jsonValueMarker = "\x00"

func (attr spanAttribute) isJSON() bool {
	return strings.HasPrefix(attr.val, jsonValueMarker)
}

func (attr spanAttribute) json() json.RawMessage {
	if attr.isJSON() {
		return json.RawMessage(attr.val[len(jsonValueMarker):])
	}
	payload, err := json.Marshal(attr.val)
	if err != nil {
		// strings always marshal
		panic(err)
	}
	return payload
}

func (attrs SpanAttributes) find(name string) int {
	for i, attr := range attrs {
		if attr.name == name {
			return i
		}
	}
	return -1
}

// Get returns the JSON encoding of the named attribute's value.
func (attrs SpanAttributes) Get(name string) (json.RawMessage, bool) {
	i := attrs.find(name)
	if i < 0 {
		return nil, false
	}
	return attrs[i].json(), true
}

// String returns the named attribute's value if it is a string.
func (attrs SpanAttributes) String(name string) (string, bool) {
	i := attrs.find(name)
	if i < 0 {
		return "", false
	}
	if !attrs[i].isJSON() {
		return attrs[i].val, true
	}
	var str string
	if err := json.Unmarshal(attrs[i].json(), &str); err != nil {
		return "", false
	}
	return str, true
}

// All iterates over the attributes and the JSON encodings of their values,
// in name order.
func (attrs SpanAttributes) All() iter.Seq2[string, json.RawMessage] {
	return func(yield func(string, json.RawMessage) bool) {
		for _, attr := range attrs.sorted() {
			if !yield(attr.name, attr.json()) {
				return
			}
		}
	}
}

func (attrs SpanAttributes) sorted() SpanAttributes {
	if slices.IsSortedFunc(attrs, compareAttrNames) {
		return attrs
	}
	return slices.SortedFunc(slices.Values(attrs), compareAttrNames)
}

func compareAttrNames(a, b spanAttribute) int {
	return strings.Compare(a.name, b.name)
}

// SetJSON sets the named attribute to a value given as its JSON encoding.
func (attrs *SpanAttributes) SetJSON(name string, payload json.RawMessage) {
	attrs.set(spanAttribute{name: internAttrName(name), val: jsonValueMarker + string(payload)})
}

// setValue sets the named attribute to an OTel attribute value, as returned
// by attribute.Value.AsInterface.
func (attrs *SpanAttributes) setValue(name string, val any) error {
	attr := spanAttribute{name: internAttrName(name)}
	if str, ok := val.(string); ok && !strings.HasPrefix(str, jsonValueMarker) {
		attr.val = internAttrValue(name, str)
	} else {
		payload, err := json.Marshal(val)
		if err != nil {
			return err
		}
		attr.val = jsonValueMarker + string(payload)
	}
	attrs.set(attr)
	return nil
}

func (attrs *SpanAttributes) set(attr spanAttribute) {
	if i := attrs.find(attr.name); i >= 0 {
		(*attrs)[i] = attr
		return
	}
	// Grow one at a time: a span's attributes arrive in a single batch and
	// are kept for the life of the DB, so slack capacity would be paid for
	// by every span. The copies are short-lived garbage.
	grown := make(SpanAttributes, len(*attrs)+1)
	copy(grown, *attrs)
	grown[len(*attrs)] = attr
	*attrs = grown
}

func (attrs SpanAttributes) MarshalJSON() ([]byte, error) {
	if attrs == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, attr := range attrs.sorted() {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(attr.name)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(attr.json())
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (attrs *SpanAttributes) UnmarshalJSON(p []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(p, &m); err != nil {
		return err
	}
	if m == nil {
		*attrs = nil
		return nil
	}
	decoded := make(SpanAttributes, 0, len(m))
	for name, payload := range m {
		decoded = append(decoded, spanAttribute{
			name: internAttrName(name),
			val:  jsonValueMarker + string(payload),
		})
	}
	slices.SortFunc(decoded, compareAttrNames)
	*attrs = decoded
	return nil
}

func (attrs SpanAttributes) GobEncode() ([]byte, error) {
	return attrs.MarshalJSON()
}

func (attrs *SpanAttributes) GobDecode(p []byte) error {
	return attrs.UnmarshalJSON(p)
}

// maxInternedAttrStrings bounds the attribute intern table, so a producer
// minting attribute names (or values of the keys below) can't grow it
// forever. Past it, strings are kept as they came.
const maxInternedAttrStrings = 4096

var attrStrings = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func internAttrString(s string) string {
	attrStrings.Lock()
	defer attrStrings.Unlock()
	if interned, ok := attrStrings.m[s]; ok {
		return interned
	}
	if len(attrStrings.m) >= maxInternedAttrStrings {
		return s
	}
	s = strings.Clone(s)
	attrStrings.m[s] = s
	return s
}

// internAttrName interns an attribute name: there are few distinct names,
// each repeated on span after span, and a decoded span brings its own copy.
func internAttrName(name string) string {
	return internAttrString(name)
}

// lowCardinalityAttrs are attributes whose string values repeat across many
// spans, so each span need not keep its own copy.
var lowCardinalityAttrs = map[string]bool{
	telemetryattrs.TelemetryOriginClientIDAttr: true,
	telemetryattrs.WcprofOpKindAttr:            true,
	telemetryattrs.CacheContractAttr:           true,
	telemetryattrs.CacheOutcomeAttr:            true,
	telemetryattrs.CacheHitRouteAttr:           true,
	telemetryattrs.CacheTypeAttr:               true,
}

func internAttrValue(name, val string) string {
	if !lowCardinalityAttrs[name] {
		return val
	}
	return internAttrString(val)
}
