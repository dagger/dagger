package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type jsonvalueSchema struct{}

var _ SchemaResolvers = &jsonvalueSchema{}

func (s jsonvalueSchema) Install(srv *dagql.Server) {
	// Top-level constructor: json (creates new empty JSONValue)
	dagql.Fields[*core.Query]{
		dagql.Func("json", s.newJSONValue).
			Doc("Initialize a JSON value"),
	}.Install(srv)

	// JSONValue methods
	dagql.Fields[*core.JSONValue]{
		dagql.Func("contents", s.contents).Doc("Return the value encoded as json").Args(
			dagql.Arg("pretty").Doc("Pretty-print"),
			dagql.Arg("indent").Doc("Optional line prefix"),
		),
		dagql.Func("withContents", s.withContents).Doc("Return a new json value, decoded from the given content").Args(
			dagql.Arg("contents").Doc("New JSON-encoded contents"),
		),
		dagql.Func("newInteger", s.newInteger).Doc("Encode an integer to json").Args(
			dagql.Arg("value").Doc("New integer value"),
		),
		dagql.Func("asInteger", s.asInteger).Doc("Decode an integer from json"),
		dagql.Func("newString", s.newString).Doc("Encode a string to json").Args(
			dagql.Arg("value").Doc("New string value"),
		),
		dagql.Func("asString", s.asString).Doc("Decode a string from json"),
		dagql.Func("newBoolean", s.newBoolean).Doc("Encode a boolean to json").Args(
			dagql.Arg("value").Doc("New boolean value"),
		),
		dagql.Func("asBoolean", s.asBoolean).Doc("Decode a boolean from json"),

		dagql.Func("asArray", s.asArray).Doc("Decode an array from json"),
		dagql.Func("fields", s.fields).Doc("List fields of the encoded object"),
		dagql.Func("field", s.field).Doc("Lookup the field at the given path, and return its value.").Args(
			dagql.Arg("path").Doc("Path of the field to lookup, encoded as an array of field names"),
		),
		dagql.Func("withField", s.withField).Doc("Set a new field at the given path").Args(
			dagql.Arg("path").Doc("Path of the field to set, encoded as an array of field names"),
			dagql.Arg("value").Doc("The new value of the field"),
		),
	}.Install(srv)
}

func (s jsonvalueSchema) newJSONValue(ctx context.Context, q *core.Query, args struct{}) (*core.JSONValue, error) {
	data, err := json.Marshal(nil)
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{Data: data}, nil
}

func (s jsonvalueSchema) contents(ctx context.Context, obj *core.JSONValue, args struct {
	Pretty dagql.Optional[dagql.Boolean] `default:"false"`
	Indent dagql.Optional[dagql.String]  `default:"  "`
}) (core.JSON, error) {
	if args.Pretty.Valid && args.Pretty.Value.Bool() {
		var v any
		if err := json.Unmarshal(obj.Data, &v); err != nil {
			return nil, err
		}

		indent := ""
		if args.Indent.Valid {
			indent = args.Indent.Value.String()
		}

		formatted, err := json.MarshalIndent(v, "", indent)
		if err != nil {
			return nil, err
		}
		return core.JSON(formatted), nil
	}
	return core.JSON(obj.Data), nil
}

func (s jsonvalueSchema) withContents(ctx context.Context, obj *core.JSONValue, args struct {
	Contents core.JSON
}) (*core.JSONValue, error) {
	// Validate JSON
	var v any
	if err := json.Unmarshal([]byte(args.Contents), &v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &core.JSONValue{Data: []byte(args.Contents)}, nil
}

func (s jsonvalueSchema) newInteger(ctx context.Context, obj *core.JSONValue, args struct {
	Value dagql.Int
}) (*core.JSONValue, error) {
	data, err := json.Marshal(args.Value.Int())
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{Data: data}, nil
}

func (s jsonvalueSchema) asInteger(ctx context.Context, obj *core.JSONValue, args struct{}) (dagql.Int, error) {
	var v int
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return 0, err
	}
	return dagql.Int(v), nil
}

func (s jsonvalueSchema) newString(ctx context.Context, obj *core.JSONValue, args struct {
	Value dagql.String
}) (*core.JSONValue, error) {
	data, err := json.Marshal(args.Value.String())
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{Data: data}, nil
}

func (s jsonvalueSchema) asString(ctx context.Context, obj *core.JSONValue, args struct{}) (dagql.String, error) {
	var v string
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return "", err
	}
	return dagql.String(v), nil
}

func (s jsonvalueSchema) newBoolean(ctx context.Context, obj *core.JSONValue, args struct {
	Value dagql.Boolean
}) (*core.JSONValue, error) {
	data, err := json.Marshal(args.Value.Bool())
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{Data: data}, nil
}

func (s jsonvalueSchema) asBoolean(ctx context.Context, obj *core.JSONValue, args struct{}) (dagql.Boolean, error) {
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

func (s jsonvalueSchema) asArray(ctx context.Context, obj *core.JSONValue, args struct{}) ([]*core.JSONValue, error) {
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value is not an array")
	}

	result := make([]*core.JSONValue, len(arr))
	for i, item := range arr {
		data, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		result[i] = &core.JSONValue{Data: data}
	}
	return result, nil
}

func (s jsonvalueSchema) fields(ctx context.Context, obj *core.JSONValue, args struct{}) ([]dagql.String, error) {
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not an object")
	}

	result := make([]dagql.String, 0, len(m))
	for key := range m {
		result = append(result, dagql.String(key))
	}
	return result, nil
}

func (s jsonvalueSchema) field(ctx context.Context, obj *core.JSONValue, args struct {
	Path []dagql.String
}) (*core.JSONValue, error) {
	var v any
	if err := json.Unmarshal(obj.Data, &v); err != nil {
		return nil, err
	}

	current := v
	for _, pathSegment := range args.Path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("can't lookup field '%s' in non-object value", pathSegment.String())
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
	return &core.JSONValue{Data: data}, nil
}

func (s jsonvalueSchema) withField(ctx context.Context, obj *core.JSONValue, args struct {
	Path  []dagql.String
	Value core.JSONValueID
}) (*core.JSONValue, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(obj.Data, &root); err != nil {
		return nil, err
	}
	// Ensure root is an object
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
	// Navigate to the parent of the target field
	current := rootMap
	for i, pathSegment := range args.Path {
		key := pathSegment.String()
		if i == len(args.Path)-1 {
			// Set the final value
			current[key] = setValue
		} else {
			// Navigate deeper, creating objects as needed
			if val, exists := current[key]; exists {
				if m, ok := val.(map[string]any); ok {
					current = m
				} else {
					// Replace non-object with new object
					newMap := make(map[string]any)
					current[key] = newMap
					current = newMap
				}
			} else {
				// Create new object
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
	return &core.JSONValue{Data: data}, nil
}
