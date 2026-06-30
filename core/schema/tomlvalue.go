package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creachadair/tomledit"
	"github.com/creachadair/tomledit/parser"
	"github.com/creachadair/tomledit/transform"
	neontoml "github.com/neongreen/mono/lib/toml"
	pelletier "github.com/pelletier/go-toml"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/tomlpath"
	"github.com/dagger/dagger/dagql"
)

type tomlvalueSchema struct{}

var _ SchemaResolvers = &tomlvalueSchema{}

func (s tomlvalueSchema) Install(srv *dagql.Server) {
	// Top-level constructor: toml (creates new empty TOMLValue)
	dagql.Fields[*core.Query]{
		dagql.Func("toml", s.newTOMLValue).
			View(AfterVersion("v1.0.0-0")).
			Doc("Initialize a TOML value"),
	}.Install(srv)

	// Gate the whole type behind the 1.0 view so it (and the Env/Binding
	// as<Type>/with<Type> accessors derived from it) stays out of the base schema.
	srv.InstallObject(dagql.NewClass[*core.TOMLValue](srv).View(AfterVersion("v1.0.0-0")))

	// TOMLValue methods
	dagql.Fields[*core.TOMLValue]{
		dagql.Func("contents", s.contents).Doc("Return the value encoded as TOML"),
		dagql.Func("withContents", s.withContents).Doc("Return a new TOML value, decoded from the given content").Args(
			dagql.Arg("contents").Doc("New TOML-encoded contents"),
		),
		dagql.Func("newInteger", s.newInteger).Doc("Encode an integer to TOML").Args(
			dagql.Arg("value").Doc("New integer value"),
		),
		dagql.Func("asInteger", s.asInteger).Doc("Decode an integer from TOML"),
		dagql.Func("newString", s.newString).Doc("Encode a string to TOML").Args(
			dagql.Arg("value").Doc("New string value"),
		),
		dagql.Func("asString", s.asString).Doc("Decode a string from TOML"),
		dagql.Func("newBoolean", s.newBoolean).Doc("Encode a boolean to TOML").Args(
			dagql.Arg("value").Doc("New boolean value"),
		),
		dagql.Func("asBoolean", s.asBoolean).Doc("Decode a boolean from TOML"),

		dagql.Func("asArray", s.asArray).Doc("Decode an array from TOML"),
		dagql.Func("fields", s.fields).Doc("List fields of the encoded table"),
		dagql.Func("field", s.field).Doc("Lookup the field at the given path, and return its value.").Args(
			dagql.Arg("path").Doc("Path of the field to lookup, encoded as an array of field names"),
		),
		dagql.Func("withField", s.withField).Doc("Set a new field at the given path, preserving the existing formatting").Args(
			dagql.Arg("path").Doc("Path of the field to set, encoded as an array of field names"),
			dagql.Arg("value").Doc("The new value of the field"),
		),
	}.Install(srv)
}

func (s tomlvalueSchema) newTOMLValue(ctx context.Context, q *core.Query, args struct{}) (*core.TOMLValue, error) {
	// An empty TOML document is an empty table.
	return &core.TOMLValue{Data: []byte("{}"), Source: []byte{}}, nil
}

func (s tomlvalueSchema) contents(ctx context.Context, obj *core.TOMLValue, args struct{}) (core.TOML, error) {
	// Preserve the original formatting when we have it.
	if obj.Source != nil {
		return core.TOML(obj.Source), nil
	}

	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return "", err
	}
	if m, ok := v.(map[string]any); ok {
		encoded, err := tomlEncode(m)
		if err != nil {
			return "", err
		}
		return core.TOML(encoded), nil
	}
	// Scalars and arrays are encoded as a bare TOML value literal, mirroring
	// JSONValue.contents which returns e.g. "5" for an integer.
	literal, err := tomlEncodeLiteral(v)
	if err != nil {
		return "", err
	}
	return core.TOML(literal), nil
}

func (s tomlvalueSchema) withContents(ctx context.Context, obj *core.TOMLValue, args struct {
	Contents core.TOML
}) (*core.TOMLValue, error) {
	data, err := tomlSourceToData([]byte(args.Contents))
	if err != nil {
		return nil, err
	}
	return &core.TOMLValue{Data: data, Source: []byte(args.Contents)}, nil
}

func (s tomlvalueSchema) newInteger(ctx context.Context, obj *core.TOMLValue, args struct {
	Value dagql.Int
}) (*core.TOMLValue, error) {
	data, err := json.Marshal(args.Value.Int())
	if err != nil {
		return nil, err
	}
	return &core.TOMLValue{Data: data}, nil
}

func (s tomlvalueSchema) asInteger(ctx context.Context, obj *core.TOMLValue, args struct{}) (dagql.Int, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return 0, err
	}
	i, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("value is not an integer")
	}
	return dagql.Int(i), nil
}

func (s tomlvalueSchema) newString(ctx context.Context, obj *core.TOMLValue, args struct {
	Value dagql.String
}) (*core.TOMLValue, error) {
	data, err := json.Marshal(args.Value.String())
	if err != nil {
		return nil, err
	}
	return &core.TOMLValue{Data: data}, nil
}

func (s tomlvalueSchema) asString(ctx context.Context, obj *core.TOMLValue, args struct{}) (dagql.String, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return dagql.String(x), nil
	// TOML date-time types have no dedicated accessor; expose their literal
	// representation as a string.
	case time.Time, pelletier.LocalDateTime, pelletier.LocalDate, pelletier.LocalTime:
		literal, err := tomlEncodeLiteral(x)
		if err != nil {
			return "", err
		}
		return dagql.String(literal), nil
	default:
		return "", fmt.Errorf("value is not a string")
	}
}

func (s tomlvalueSchema) newBoolean(ctx context.Context, obj *core.TOMLValue, args struct {
	Value dagql.Boolean
}) (*core.TOMLValue, error) {
	data, err := json.Marshal(args.Value.Bool())
	if err != nil {
		return nil, err
	}
	return &core.TOMLValue{Data: data}, nil
}

func (s tomlvalueSchema) asBoolean(ctx context.Context, obj *core.TOMLValue, args struct{}) (dagql.Boolean, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("value is not a boolean")
	}
	return dagql.Boolean(b), nil
}

func (s tomlvalueSchema) asArray(ctx context.Context, obj *core.TOMLValue, args struct{}) ([]*core.TOMLValue, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value is not an array")
	}

	result := make([]*core.TOMLValue, len(arr))
	for i, item := range arr {
		data, err := tomlMarshalData(item)
		if err != nil {
			return nil, err
		}
		result[i] = &core.TOMLValue{Data: data}
	}
	return result, nil
}

func (s tomlvalueSchema) fields(ctx context.Context, obj *core.TOMLValue, args struct{}) ([]dagql.String, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not a table")
	}

	result := make([]dagql.String, 0, len(m))
	for key := range m {
		result = append(result, dagql.String(key))
	}
	return result, nil
}

func (s tomlvalueSchema) field(ctx context.Context, obj *core.TOMLValue, args struct {
	Path []dagql.String
}) (*core.TOMLValue, error) {
	v, err := tomlDecodeData(obj.Data)
	if err != nil {
		return nil, err
	}

	current := v
	for _, pathSegment := range args.Path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("can't lookup field '%s' in non-table value", pathSegment.String())
		}
		val, exists := m[pathSegment.String()]
		if !exists {
			return nil, fmt.Errorf("no such field: '%s'", pathSegment.String())
		}
		current = val
	}

	data, err := tomlMarshalData(current)
	if err != nil {
		return nil, err
	}
	return &core.TOMLValue{Data: data}, nil
}

func (s tomlvalueSchema) withField(ctx context.Context, obj *core.TOMLValue, args struct {
	Path  []dagql.String
	Value core.TOMLValueID
}) (*core.TOMLValue, error) {
	if len(args.Path) == 0 {
		return nil, fmt.Errorf("path must not be empty")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	root, err := tomlDecodeData(obj.Data)
	if err != nil {
		return nil, err
	}
	// Ensure root is a table
	rootMap, ok := root.(map[string]any)
	if !ok {
		rootMap = make(map[string]any)
	}

	// Get the value to set
	value, err := args.Value.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	setValue, err := tomlDecodeData(value.Self().Data)
	if err != nil {
		return nil, err
	}

	// Update the value model: navigate to the parent of the target field,
	// creating tables as needed.
	current := rootMap
	for i, pathSegment := range args.Path {
		key := pathSegment.String()
		if i == len(args.Path)-1 {
			current[key] = setValue
		} else {
			if val, exists := current[key]; exists {
				if m, ok := val.(map[string]any); ok {
					current = m
				} else {
					newMap := make(map[string]any)
					current[key] = newMap
					current = newMap
				}
			} else {
				newMap := make(map[string]any)
				current[key] = newMap
				current = newMap
			}
		}
	}
	data, err := tomlMarshalData(rootMap)
	if err != nil {
		return nil, err
	}

	// Update the TOML source, preserving formatting when possible.
	source, err := tomlApplyField(obj.Source, args.Path, setValue, rootMap)
	if err != nil {
		return nil, err
	}

	return &core.TOMLValue{Data: data, Source: source}, nil
}

// tomlApplyField produces the new TOML source for a withField edit. When an
// existing source is available it edits it in place via a comment-preserving
// editor; otherwise (or if the in-place edit fails) it re-encodes the whole
// value model.
func tomlApplyField(source []byte, path []dagql.String, setValue any, rootMap map[string]any) ([]byte, error) {
	if source == nil {
		// No source to preserve; leave it lazily encoded by contents().
		return nil, nil
	}

	if doc, err := neontoml.Parse(source); err == nil {
		if err := doc.Set(tomlDottedPath(path), setValue); err == nil {
			return doc.Bytes(), nil
		}
	}
	// The editor cannot format some TOML-native types (date-times, special
	// floats); retry the in-place edit with a pre-rendered raw literal.
	if literal, err := tomlEncodeLiteral(setValue); err == nil {
		if out, ok := tomlSetRawLiteral(source, path, literal); ok {
			return out, nil
		}
	}

	// Fall back to re-encoding the whole value model.
	encoded, err := tomlEncode(rootMap)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// tomlSetRawLiteral edits source in place, setting the field at path to a raw
// TOML value literal. It handles updating an existing key, and inserting a new
// key at the top level or into an existing table. It reports false when the
// edit cannot be applied (the caller falls back to re-encoding).
func tomlSetRawLiteral(source []byte, path []dagql.String, literal string) ([]byte, bool) {
	doc, err := tomledit.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, false
	}
	keys, err := parser.ParseKey(tomlDottedPath(path))
	if err != nil {
		return nil, false
	}
	val, err := parser.ParseValue(literal)
	if err != nil {
		return nil, false
	}

	switch entry := doc.First(keys...); {
	case entry != nil && entry.KeyValue != nil:
		val.Trailer = entry.KeyValue.Value.Trailer
		entry.KeyValue.Value = val
	case len(keys) == 1:
		if doc.Global == nil {
			doc.Global = &tomledit.Section{}
		}
		transform.InsertMapping(doc.Global, &parser.KeyValue{Name: keys, Value: val}, true)
	default:
		table := transform.FindTable(doc, keys[:len(keys)-1]...)
		if table == nil {
			return nil, false
		}
		transform.InsertMapping(table.Section, &parser.KeyValue{Name: keys[len(keys)-1:], Value: val}, true)
	}

	var buf bytes.Buffer
	var formatter tomledit.Formatter
	if err := formatter.Format(&buf, doc); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// tomlSourceToData decodes TOML text into the JSON value model.
func tomlSourceToData(src []byte) ([]byte, error) {
	tree, err := pelletier.LoadBytes(src)
	if err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	return tomlMarshalData(tree.ToMap())
}

// The JSON value model cannot represent every TOML type: date-times would be
// silently re-typed as strings, and json.Marshal rejects inf/nan floats
// outright. Such values are stored as a sentinel wrapper object instead:
//
//	{"$dagger.toml": "<kind>", "value": "<literal>"}
//
// tomlMarshalData writes the wrappers and tomlDecodeData restores the typed
// values, so the rest of the resolvers only ever see proper Go values.
const tomlTypeKey = "$dagger.toml"

const (
	tomlKindDatetime      = "datetime"
	tomlKindLocalDatetime = "local-datetime"
	tomlKindLocalDate     = "local-date"
	tomlKindLocalTime     = "local-time"
	tomlKindFloat         = "float"
	// tomlKindTable escapes a genuine table that happens to collide with the
	// wrapper shape.
	tomlKindTable = "table"
)

func tomlWrap(kind, literal string) map[string]any {
	return map[string]any{tomlTypeKey: kind, "value": literal}
}

// tomlMarshalData encodes a decoded TOML value into the JSON value model.
func tomlMarshalData(v any) ([]byte, error) {
	return json.Marshal(tomlToJSONModel(v))
}

func tomlToJSONModel(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = tomlToJSONModel(val)
		}
		if _, collides := out[tomlTypeKey]; collides {
			return map[string]any{tomlTypeKey: tomlKindTable, "value": out}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = tomlToJSONModel(val)
		}
		return out
	case time.Time:
		return tomlWrap(tomlKindDatetime, x.Format(time.RFC3339Nano))
	case pelletier.LocalDateTime:
		return tomlWrap(tomlKindLocalDatetime, x.String())
	case pelletier.LocalDate:
		return tomlWrap(tomlKindLocalDate, x.String())
	case pelletier.LocalTime:
		return tomlWrap(tomlKindLocalTime, x.String())
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			literal, _ := tomlFormatFloat(x)
			return tomlWrap(tomlKindFloat, literal)
		}
		return x
	default:
		return v
	}
}

// tomlDecodeData decodes the JSON value model into a Go value suitable for TOML
// encoding, preserving integers (which the encoding/json default would widen to
// float64) and restoring TOML types stored as sentinel wrappers.
func tomlDecodeData(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return tomlFromJSONModel(v)
}

func tomlFromJSONModel(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		if kind, ok := x[tomlTypeKey].(string); ok && len(x) == 2 {
			if wrapped, ok := x["value"]; ok {
				return tomlUnwrap(kind, wrapped)
			}
		}
		for k, val := range x {
			decoded, err := tomlFromJSONModel(val)
			if err != nil {
				return nil, err
			}
			x[k] = decoded
		}
		return x, nil
	case []any:
		for i, val := range x {
			decoded, err := tomlFromJSONModel(val)
			if err != nil {
				return nil, err
			}
			x[i] = decoded
		}
		return x, nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		if f, err := x.Float64(); err == nil {
			return f, nil
		}
		return x.String(), nil
	default:
		return v, nil
	}
}

func tomlUnwrap(kind string, wrapped any) (any, error) {
	if kind == tomlKindTable {
		m, ok := wrapped.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed TOML table wrapper")
		}
		return tomlFromJSONModel(m)
	}

	literal, ok := wrapped.(string)
	if !ok {
		return nil, fmt.Errorf("malformed TOML %s wrapper", kind)
	}
	switch kind {
	case tomlKindDatetime:
		t, err := time.Parse(time.RFC3339Nano, literal)
		if err != nil {
			return nil, fmt.Errorf("malformed TOML datetime %q: %w", literal, err)
		}
		return t, nil
	case tomlKindLocalDatetime:
		var ldt pelletier.LocalDateTime
		if err := ldt.UnmarshalText([]byte(literal)); err != nil {
			return nil, fmt.Errorf("malformed TOML local datetime %q: %w", literal, err)
		}
		return ldt, nil
	case tomlKindLocalDate:
		var ld pelletier.LocalDate
		if err := ld.UnmarshalText([]byte(literal)); err != nil {
			return nil, fmt.Errorf("malformed TOML local date %q: %w", literal, err)
		}
		return ld, nil
	case tomlKindLocalTime:
		var lt pelletier.LocalTime
		if err := lt.UnmarshalText([]byte(literal)); err != nil {
			return nil, fmt.Errorf("malformed TOML local time %q: %w", literal, err)
		}
		return lt, nil
	case tomlKindFloat:
		switch literal {
		case "inf", "+inf":
			return math.Inf(1), nil
		case "-inf":
			return math.Inf(-1), nil
		case "nan":
			return math.NaN(), nil
		}
		return nil, fmt.Errorf("malformed TOML float literal %q", literal)
	}
	return nil, fmt.Errorf("unknown TOML wrapper kind %q", kind)
}

// tomlEncode encodes a Go value to TOML text. Only tables (maps) can be encoded
// as a TOML document.
func tomlEncode(v any) ([]byte, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cannot encode non-table value as TOML")
	}
	tree, err := pelletier.TreeFromMap(m)
	if err != nil {
		return nil, err
	}
	out, err := tree.ToTomlString()
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// tomlEncodeLiteral encodes a decoded TOML value as a bare TOML value literal:
// scalars and date-times as their literal form, arrays inline, and tables as
// inline tables.
func tomlEncodeLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("cannot encode null as a TOML value")
	case string:
		return tomlQuoteString(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return tomlFormatFloat(x)
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	case pelletier.LocalDateTime:
		return x.String(), nil
	case pelletier.LocalDate:
		return x.String(), nil
	case pelletier.LocalTime:
		return x.String(), nil
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			s, err := tomlEncodeLiteral(item)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			s, err := tomlEncodeLiteral(x[k])
			if err != nil {
				return "", err
			}
			parts[i] = tomlpath.FormatSegment(k) + " = " + s
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", fmt.Errorf("cannot encode %T as a TOML value", v)
	}
}

func tomlFormatFloat(f float64) (string, error) {
	switch {
	case math.IsInf(f, 1):
		return "inf", nil
	case math.IsInf(f, -1):
		return "-inf", nil
	case math.IsNaN(f):
		return "nan", nil
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s, nil
}

// tomlQuoteString renders s as a TOML basic string. JSON string escaping is a
// subset of TOML basic string escaping, so reuse it.
func tomlQuoteString(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string cannot fail.
		panic(err)
	}
	return string(quoted)
}

// tomlDottedPath builds a TOML dotted-key path from field name segments. It
// delegates to the shared tomlpath formatter so that an edit produces the same
// TOML output as the engine's other format-preserving TOML writers.
func tomlDottedPath(segments []dagql.String) string {
	parts := make([]string, len(segments))
	for i, seg := range segments {
		parts[i] = seg.String()
	}
	return tomlpath.Dotted(parts...)
}
