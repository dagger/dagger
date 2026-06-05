package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

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
			Doc("Initialize a TOML value"),
	}.Install(srv)

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
	encoded, err := tomlEncode(v)
	if err != nil {
		return "", err
	}
	return core.TOML(encoded), nil
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
	var v int
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return 0, err
	}
	return dagql.Int(v), nil
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
	var v string
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return "", err
	}
	return dagql.String(v), nil
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
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("value is not a boolean")
	}
	return dagql.Boolean(b), nil
}

func (s tomlvalueSchema) asArray(ctx context.Context, obj *core.TOMLValue, args struct{}) ([]*core.TOMLValue, error) {
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value is not an array")
	}

	result := make([]*core.TOMLValue, len(arr))
	for i, item := range arr {
		data, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		result[i] = &core.TOMLValue{Data: data}
	}
	return result, nil
}

func (s tomlvalueSchema) fields(ctx context.Context, obj *core.TOMLValue, args struct{}) ([]dagql.String, error) {
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
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
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
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

	data, err := json.Marshal(current)
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

	var root any
	if err := json.Unmarshal(obj.Data, &root); err != nil {
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
	var setValue any
	if err := json.Unmarshal(value.Self().Data, &setValue); err != nil {
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
	data, err := json.Marshal(rootMap)
	if err != nil {
		return nil, err
	}

	// Update the TOML source, preserving formatting when possible.
	source, err := tomlApplyField(obj.Source, args.Path, value.Self().Data, rootMap)
	if err != nil {
		return nil, err
	}

	return &core.TOMLValue{Data: data, Source: source}, nil
}

// tomlApplyField produces the new TOML source for a withField edit. When an
// existing source is available it edits it in place via a comment-preserving
// editor; otherwise (or if the in-place edit fails) it re-encodes the whole
// value model.
func tomlApplyField(source []byte, path []dagql.String, valueData []byte, rootMap map[string]any) ([]byte, error) {
	if source != nil {
		setValue, err := tomlDecodeData(valueData)
		if err != nil {
			return nil, err
		}
		if doc, err := neontoml.Parse(source); err == nil {
			if err := doc.Set(tomlDottedPath(path), setValue); err == nil {
				return doc.Bytes(), nil
			}
		}
		// Fall back to re-encoding the value model below.
	} else {
		// No source to preserve; leave it lazily encoded by contents().
		return nil, nil
	}

	encoded, err := tomlEncode(rootMap)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// tomlSourceToData decodes TOML text into the JSON value model.
func tomlSourceToData(src []byte) ([]byte, error) {
	tree, err := pelletier.LoadBytes(src)
	if err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	return json.Marshal(tree.ToMap())
}

// tomlDecodeData decodes the JSON value model into a Go value suitable for TOML
// encoding, preserving integers (which the encoding/json default would widen to
// float64).
func tomlDecodeData(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return tomlNormalizeNumbers(v), nil
}

func tomlNormalizeNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = tomlNormalizeNumbers(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = tomlNormalizeNumbers(val)
		}
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	default:
		return v
	}
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
