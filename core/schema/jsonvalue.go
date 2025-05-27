package schema

import (
	"context"
	"fmt"
	"encoding/json"

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
		dagql.Func("jsonvalue", s.newJSONValue).
			Doc("Create a new empty JSONValue"),
	}.Install(s.srv)

	// Expose methods for JSONValue manipulation
	dagql.Fields[*core.JSONValue]{
		dagql.Func("unset", s.unset).Doc("Removes the value at the specified path. Empty path resets to null.").Args(
			dagql.Arg("path").Doc("The JSON path (dot-separated)")
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

func (s jsonvalueSchema) unset(ctx context.Context, obj *core.JSONValue, args struct{ Path string }) (*core.JSONValue, error) {
	newobj, err := obj.data.Unset(args.Path)
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{data: newobj}, nil
}

func (s jsonvalueSchema) setString(ctx context.Context, obj *core.JSONValue, args struct{ Path, Value string }) (*core.JSONValue, error) {
	return s.setValue(obj, args.Path, args.Value)
}

func (s jsonvalueSchema) setInteger(ctx context.Context, obj *core.JSONValue, args struct{ Path string; Value int }) (*core.JSONValue, error) {
	return s.setValue(obj, args.Path, args.Value)
}

func (s jsonvalueSchema) setBoolean(ctx context.Context, obj *core.JSONValue, args struct{ Path string; Value bool }) (*core.JSONValue, error) {
	return s.setValue(obj, args.Path, args.Value)
}

func (s jsonvalueSchema) setJSON(ctx context.Context, obj *core.JSONValue, args struct{ Path string; Value core.JSON }) (*core.JSONValue, error) {
	var raw any
	if err := json.Unmarshal([]byte(args.Value), &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return s.setValue(obj, args.Path, raw)
}

func (s jsonvalueSchema) setValue(obj *core.JSONValue, path string, value any) (*core.JSONValue, error) {
	newobj, err := obj.data.Set(path, value)
	if err != nil {
		return nil, err
	}
	return &core.jsonvalue{data: newobj}, nil
}
