package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type envfileSchema struct{}

var _ SchemaResolvers = &envfileSchema{}

func (s envfileSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("envFile", s.newEnvFile).
			Doc("Initialize an environment file"),
	}.Install(srv)

	dagql.Fields[*core.EnvFile]{
		dagql.Func("variables", s.variables).
			Doc("Return all variables"),
		dagql.Func("withVariable", s.withVariable).
			Doc("Add a variable").
			Args(
				dagql.Arg("name").
					Doc("Variable name"),
				dagql.Arg("value").
					Doc("Variable value"),
			),
		dagql.Func("withoutVariable", s.withoutVariable).
			Doc("Remove all occurrences of the named variable").
			Args(
				dagql.Arg("name").
					Doc("Variable name to remove"),
			),
		dagql.Func("variable", s.variable).
			Doc("Lookup a variable by name (last occurrence wins)").
			Args(
				dagql.Arg("name").
					Doc("Variable name to lookup"),
			),
		dagql.Func("file", s.file).
			Doc("Return the contents as a file"),
	}.Install(srv)

	dagql.Fields[*core.File]{
		dagql.Func("asEnvFile", s.asEnvFile).
			Doc("Parse as an env file"),
	}.Install(srv)
}

func (s envfileSchema) newEnvFile(ctx context.Context, q *core.Query, args struct{}) (*core.EnvFile, error) {
	return &core.EnvFile{}, nil
}

func (s envfileSchema) variables(ctx context.Context, obj *core.EnvFile, args struct{}) (dagql.Array[core.EnvVariable], error) {
	return obj.Variables(), nil
}

func (s envfileSchema) withVariable(ctx context.Context, obj *core.EnvFile, args struct {
	Name  dagql.String
	Value dagql.String
}) (*core.EnvFile, error) {
	return obj.WithVariable(args.Name.String(), args.Value.String(), false), nil
}

func (s envfileSchema) withoutVariable(ctx context.Context, obj *core.EnvFile, args struct {
	Name dagql.String
}) (*core.EnvFile, error) {
	return obj.WithoutVariable(args.Name.String()), nil
}

func (s envfileSchema) variable(ctx context.Context, obj *core.EnvFile, args struct {
	Name dagql.String
}) (dagql.Nullable[dagql.String], error) {
	none := dagql.Null[dagql.String]()
	if val, ok := obj.Variable(args.Name.String()); ok {
		return dagql.NonNull(dagql.NewString(val)), nil
	}
	return none, nil
}

func (s envfileSchema) file(ctx context.Context, obj *core.EnvFile, args struct{}) (*core.File, error) {
	return obj.File(ctx)
}

func (s envfileSchema) asEnvFile(ctx context.Context, obj *core.File, args struct{}) (*core.EnvFile, error) {
	return obj.AsEnvFile(ctx)
}
