package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type jsonvalueSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &jsonvalueSchema{}

func (s jsonvalueSchema) Install() {
	// Top-level constructor: jsonvalue (creates new empty jsonvalue)
	dagql.Fields[*core.Query]{
		dagql.Func("json", s.newJSONValue).
			Doc("Initialize an empty JSON value"),
	}.Install(s.srv)

	// Expose methods for JSONValue manipulation
	dagql.Fields[*core.JSONValue]{
		dagql.Func("get", s.get).Doc("Return the JSON-encoded value, or a sub-value at the given path").Args(
			dagql.Arg("path").Doc("The JSON path (dot-separated)"),
		),
		dagql.Func("unset", s.unset).Doc("Removes the value at the specified path. Empty path resets to null.").Args(
			dagql.Arg("path").Doc("The JSON path (dot-separated)"),
		),
		dagql.Func("setString", s.setString).Doc("Set a string value at the specified path.").Args(
			dagql.Arg("path"),
			dagql.Arg("value"),
		),
		dagql.Func("setInteger", s.setInteger).Doc("Set an integer value at the specified path.").Args(
			dagql.Arg("path"),
			dagql.Arg("value"),
		),
		dagql.Func("setBoolean", s.setBoolean).Doc("Set a boolean value at the specified path.").Args(
			dagql.Arg("path"),
			dagql.Arg("value"),
		),
		dagql.Func("setJSON", s.setJSON).Doc("Set a value as raw JSON at the specified path").Args(
			dagql.Arg("path"),
			dagql.Arg("value"),
		),

        dagql.Func("getString", s.getString).Doc("Get a string value at the specified path.").Args(
            dagql.Arg("path"),
        ),
        dagql.Func("getInt", s.getInt).Doc("Get an integer value at the specified path.").Args(
            dagql.Arg("path"),
        ),
        dagql.Func("getBool", s.getBool).Doc("Get a boolean value at the specified path.").Args(
            dagql.Arg("path"),
        ),
        dagql.Func("getJSON", s.getJSON).Doc("Get a value as raw JSON at the specified path.").Args(
            dagql.Arg("path"),
        ),
	}.Install(s.srv)
}

func (s jsonvalueSchema) newJSONValue(ctx context.Context, q *core.Query, args struct{}) (*core.JSONValue, error) {
	return core.NewJSONValue(nil)
}

func (s jsonvalueSchema) get(ctx context.Context, obj *core.JSONValue, args struct{ Path dagql.String }) (core.JSON, error) {
	v, err := obj.Get(args.Path.String())
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return core.JSON(data), nil
}

func (s jsonvalueSchema) unset(ctx context.Context, obj *core.JSONValue, args struct{ Path dagql.String }) (*core.JSONValue, error) {
	return obj.Unset(args.Path.String())
}

func (s jsonvalueSchema) setString(ctx context.Context, obj *core.JSONValue, args struct {
	Path  dagql.String
	Value dagql.String
}) (*core.JSONValue, error) {
	return obj.Set(args.Path.String(), args.Value.String())
}

func (s jsonvalueSchema) setInteger(ctx context.Context, obj *core.JSONValue, args struct {
	Path  dagql.String
	Value dagql.Int
}) (*core.JSONValue, error) {
	return obj.Set(args.Path.String(), args.Value.Int())
}

func (s jsonvalueSchema) setBoolean(ctx context.Context, obj *core.JSONValue, args struct {
	Path  dagql.String
	Value dagql.Boolean
}) (*core.JSONValue, error) {
	return obj.Set(args.Path.String(), args.Value.Bool())
}

func (s jsonvalueSchema) setJSON(ctx context.Context, obj *core.JSONValue, args struct {
	Path  dagql.String
	Value core.JSON
}) (*core.JSONValue, error) {
	var value any
	if err := json.Unmarshal([]byte(args.Value), &value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return obj.Set(args.Path.String(), value)
}

// Getters
func (s jsonvalueSchema) getString(ctx context.Context, obj *core.JSONValue, args struct {
	Path dagql.String
}) (dagql.String, error) {
	v, err := obj.Get(args.Path.String())
	if err != nil {
		return "", err
	}
	sv, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("value at path is not a string")
	}
	return dagql.String(sv), nil
}

func (s jsonvalueSchema) getInt(ctx context.Context, obj *core.JSONValue, args struct {
	Path dagql.String
}) (dagql.Int, error) {
	v, err := obj.Get(args.Path.String())
	if err != nil {
		return 0, err
	}
	// Use float64 for JSON numbers, convert to int
	fv, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("value at path is not a number")
	}
	return dagql.Int(int(fv)), nil
}

func (s jsonvalueSchema) getBool(ctx context.Context, obj *core.JSONValue, args struct {
	Path dagql.String
}) (dagql.Boolean, error) {
	v, err := obj.Get(args.Path.String())
	if err != nil {
		return false, err
	}
	bv, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("value at path is not a boolean")
	}
	return dagql.Boolean(bv), nil
}

func (s jsonvalueSchema) getJSON(ctx context.Context, obj *core.JSONValue, args struct {
	Path dagql.String
}) (core.JSON, error) {
	v, err := obj.Get(args.Path.String())
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return core.JSON(data), nil
}
