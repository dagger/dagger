package dagui

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	telemetry "github.com/dagger/otel-go"

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
// It marshals to and from the same JSON object a map would, and gob-encodes
// as that, or in a binary form (see GobEncode).
//
// Most of a span's attributes come from a small vocabulary of name and value
// pairs (the origin client, cache outcomes, the module called): the slice
// holds pointers, and such a pair is one shared, immutable spanAttribute
// (see internAttrPair), so a span pays a pointer for it rather than two
// string headers.
type SpanAttributes []*spanAttribute

// spanAttribute is one attribute. It is never modified once in a
// SpanAttributes: it may be shared.
type spanAttribute struct {
	name string
	// val is the attribute's value: the string itself, or for any other
	// value its JSON encoding behind a jsonValueMarker.
	val string
}

// jsonValueMarker prefixes a spanAttribute value held as JSON. A string
// value starting with it is held as JSON too, so the two never mix up.
const jsonValueMarker = "\x00"

func (attr *spanAttribute) isJSON() bool {
	return strings.HasPrefix(attr.val, jsonValueMarker)
}

func (attr *spanAttribute) json() json.RawMessage {
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

func compareAttrNames(a, b *spanAttribute) int {
	return strings.Compare(a.name, b.name)
}

// SetJSON sets the named attribute to a value given as its JSON encoding.
func (attrs *SpanAttributes) SetJSON(name string, payload json.RawMessage) {
	attrs.set(&spanAttribute{name: internAttrName(name), val: jsonValueMarker + string(payload)})
}

// setValue sets the named attribute to an OTel attribute value, as returned
// by attribute.Value.AsInterface.
func (attrs *SpanAttributes) setValue(name string, val any) error {
	if str, ok := val.(string); ok && !strings.HasPrefix(str, jsonValueMarker) {
		// A DB shares the low-cardinality ones as they arrive (see
		// DB.shareAttrPairs).
		attrs.set(&spanAttribute{name: internAttrName(name), val: str})
		return nil
	}
	payload, err := json.Marshal(val)
	if err != nil {
		return err
	}
	attrs.set(&spanAttribute{name: internAttrName(name), val: jsonValueMarker + string(payload)})
	return nil
}

func (attrs *SpanAttributes) set(attr *spanAttribute) {
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
	size := 2
	for _, attr := range attrs {
		size += len(attr.name) + len(attr.val) + 6
	}
	buf := make([]byte, 0, size)
	buf = append(buf, '{')
	for i, attr := range attrs.sorted() {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendJSONString(buf, attr.name)
		buf = append(buf, ':')
		if attr.isJSON() {
			buf = append(buf, attr.val[len(jsonValueMarker):]...)
		} else {
			buf = appendJSONString(buf, attr.val)
		}
	}
	buf = append(buf, '}')
	return buf, nil
}

// appendJSONString appends s as encoding/json encodes it. Attribute names
// and most values need no escaping, so they are copied as they are, quoted;
// the rest go through encoding/json.
func appendJSONString(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			payload, err := json.Marshal(s)
			if err != nil {
				// strings always marshal
				panic(err)
			}
			return append(buf, payload...)
		}
	}
	buf = append(buf, '"')
	buf = append(buf, s...)
	return append(buf, '"')
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
		decoded = append(decoded, &spanAttribute{
			name: internAttrName(name),
			val:  jsonValueMarker + string(payload),
		})
	}
	slices.SortFunc(decoded, compareAttrNames)
	*attrs = decoded
	return nil
}

// Gob encoding.
//
// SpanAttributes gob-encode as their JSON object, or, once
// EncodeBinaryAttributes opts in, in a binary form: a binaryAttrsVersion
// byte, the number of attributes, then each attribute's name and value as it
// is held (a string as it is, anything else as its JSON behind a
// jsonValueMarker), each prefixed with its length. Lengths and the count are
// uvarints. A remote frontend (the Cloud websocket) sends thousands of spans
// a batch, and encoding JSON on the server, and decoding it in the browser,
// dominated sending them.
//
// GobDecode reads either form: JSON starts with '{' (or is null), never
// with binaryAttrsVersion. A decoder from before the binary form can't read
// it, though: it fails with a JSON syntax error, failing the decoding of the
// whole message holding the snapshot. So a frontend still running an older
// build (a browser tab open across a deploy) must not be sent it: the binary
// form is opt-in, for a server to turn on once every decoder it talks to
// reads it. A server can't tell that from the gob stream alone: it has to
// know its frontends' builds, e.g. by turning away (or reloading) frontends
// older than a build that reads the binary form, deployed before it turns
// it on.

// binaryAttrsVersion is the first byte of SpanAttributes in binary form.
const binaryAttrsVersion = 1

var encodeBinaryAttrs atomic.Bool

// EncodeBinaryAttributes sets whether SpanAttributes gob-encode in their
// binary form (see above) rather than as JSON, for every gob stream of the
// process. Turn it on only when every decoder of them reads the binary
// form: a decoder from before it was introduced fails on it. GobDecode
// reads both forms either way.
func EncodeBinaryAttributes(on bool) {
	encodeBinaryAttrs.Store(on)
}

func (attrs SpanAttributes) GobEncode() ([]byte, error) {
	if attrs == nil || !encodeBinaryAttrs.Load() {
		return attrs.MarshalJSON()
	}
	return attrs.appendBinary(nil), nil
}

func (attrs *SpanAttributes) GobDecode(p []byte) error {
	if len(p) > 0 && p[0] == binaryAttrsVersion {
		return attrs.decodeBinary(p[1:])
	}
	return attrs.UnmarshalJSON(p)
}

// appendBinary appends the attributes' binary form to buf.
func (attrs SpanAttributes) appendBinary(buf []byte) []byte {
	size := 1 + binary.MaxVarintLen64
	for _, attr := range attrs {
		size += len(attr.name) + len(attr.val) + 2*binary.MaxVarintLen64
	}
	buf = slices.Grow(buf, size)
	buf = append(buf, binaryAttrsVersion)
	buf = binary.AppendUvarint(buf, uint64(len(attrs)))
	for _, attr := range attrs {
		buf = binary.AppendUvarint(buf, uint64(len(attr.name)))
		buf = append(buf, attr.name...)
		buf = binary.AppendUvarint(buf, uint64(len(attr.val)))
		buf = append(buf, attr.val...)
	}
	return buf
}

var errBadBinaryAttrs = errors.New("dagui: malformed binary span attributes")

// decodeBinary decodes the binary form, after its version byte. Names are
// interned, and the values share one string.
func (attrs *SpanAttributes) decodeBinary(p []byte) error {
	count, n := binary.Uvarint(p)
	// each attribute takes at least two bytes
	if n <= 0 || count > uint64(len(p)-n)/2 {
		return errBadBinaryAttrs
	}
	p = p[n:]
	// field reads a length-prefixed field off p
	field := func() ([]byte, bool) {
		l, n := binary.Uvarint(p)
		if n <= 0 || l > uint64(len(p)-n) {
			return nil, false
		}
		f := p[n : n+int(l)]
		p = p[n+int(l):]
		return f, true
	}
	type attrBytes struct{ name, val []byte }
	fields := make([]attrBytes, count)
	var valsLen int
	for i := range fields {
		name, ok := field()
		if !ok {
			return errBadBinaryAttrs
		}
		val, ok := field()
		if !ok {
			return errBadBinaryAttrs
		}
		fields[i] = attrBytes{name, val}
		valsLen += len(val)
	}
	if len(p) != 0 {
		return errBadBinaryAttrs
	}
	var vals strings.Builder
	vals.Grow(valsLen)
	for _, f := range fields {
		vals.Write(f.val)
	}
	all := vals.String()
	slab := make([]spanAttribute, count)
	decoded := make(SpanAttributes, count)
	for i, f := range fields {
		slab[i] = spanAttribute{name: internAttrBytes(f.name), val: all[:len(f.val)]}
		all = all[len(f.val):]
		decoded[i] = &slab[i]
	}
	*attrs = decoded
	return nil
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

// internAttrBytes is internAttrString for a name decoded from bytes, which
// it copies only when it's new.
func internAttrBytes(b []byte) string {
	attrStrings.Lock()
	defer attrStrings.Unlock()
	if interned, ok := attrStrings.m[string(b)]; ok {
		return interned
	}
	s := string(b)
	if len(attrStrings.m) < maxInternedAttrStrings {
		attrStrings.m[s] = s
	}
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
	// Module calls name their module (ref@version) and function, and the
	// caller's: as many values as functions called, on every call.
	telemetry.ModuleRefAttr:                    true,
	telemetry.ModuleFunctionCallNameAttr:       true,
	telemetry.ModuleCallerRefAttr:              true,
	telemetry.ModuleCallerFunctionCallNameAttr: true,
}

// maxSharedAttrPairs bounds a DB's attrPairs, so a producer minting values
// of the lowCardinalityAttrs can't grow it without limit; past it, spans
// keep their own.
const maxSharedAttrPairs = 1 << 14

// shareAttrPairs swaps the span's low-cardinality string attributes for the
// DB's shared spanAttribute of the same name and value, adopting the span's
// own when the DB has none yet. The table is the DB's, so it lives and dies
// with the session's spans: an engine building DB after DB starts each
// afresh.
func (db *DB) shareAttrPairs(attrs SpanAttributes) {
	for i, attr := range attrs {
		if !lowCardinalityAttrs[attr.name] || attr.isJSON() {
			continue
		}
		key := [2]string{attr.name, attr.val}
		if shared, ok := db.attrPairs[key]; ok {
			attrs[i] = shared
			continue
		}
		if len(db.attrPairs) >= maxSharedAttrPairs {
			continue
		}
		if db.attrPairs == nil {
			db.attrPairs = map[[2]string]*spanAttribute{}
		}
		db.attrPairs[key] = attr
	}
}
