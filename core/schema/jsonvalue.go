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
		dagql.Func("get", s.unset).Doc("Return the JSON-encoded value, or a sub-value at the given path").Args(
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
	}.Install(s.srv)
}

func (s jsonvalueSchema) newJSONValue(ctx context.Context, q *core.Query, args struct{}) (*core.JSONValue, error) {
	return &core.JSONValue{}, nil
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
