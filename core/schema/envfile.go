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
			Doc("Initialize an environment file").
			Args(
				dagql.Arg("expand").
					Doc(`Replace "${VAR}" or "$VAR" with the value of other vars`),
			),
	}.Install(srv)

	dagql.Fields[*core.EnvFile]{
		dagql.Func("variables", s.variables).
			Doc("Return all variables"),
		dagql.Func("withVariable", s.withVariable).
			Doc("Add a variable").
			Args(
				dagql.Arg("name").Doc("Variable name"),
				dagql.Arg("value").Doc("Variable value"),
			),
		dagql.Func("withoutVariable", s.withoutVariable).
			Doc("Remove all occurrences of the named variable").
			Args(
				dagql.Arg("name").Doc("Variable name"),
			),
		dagql.Func("get", s.get).
			Doc(`Lookup a variable (last occurrence wins) and return its value, or an empty string`).
			Args(
				dagql.Arg("name").Doc("Variable name"),
			),
		dagql.Func("exists", s.exists).
			Doc("Check if a variable exists").
			Args(
				dagql.Arg("name").Doc("Variable name"),
			),
		dagql.Func("asFile", s.asFile).
			Doc("Return as a file"),
	}.Install(srv)

	dagql.Fields[*core.File]{
		dagql.Func("asEnvFile", s.asEnvFile).
			Args(
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" with the value of other vars`,
				),
			).
			Doc("Parse as an env file"),
	}.Install(srv)
}

func (s envfileSchema) newEnvFile(
	ctx context.Context,
	q *core.Query,
	args struct {
		Expand dagql.Optional[dagql.Boolean]
	},
) (*core.EnvFile, error) {
	var expand bool
	if args.Expand.Valid {
		expand = args.Expand.Value.Bool()
	}
	return &core.EnvFile{Expand: expand}, nil
}

func (s envfileSchema) variables(ctx context.Context, parent *core.EnvFile, args struct{}) (dagql.Array[core.EnvVariable], error) {
	return parent.Variables(), nil
}

func (s envfileSchema) withVariable(ctx context.Context, parent *core.EnvFile, args struct {
	Name  dagql.String
	Value dagql.String
}) (*core.EnvFile, error) {
	return parent.WithVariable(args.Name.String(), args.Value.String()), nil
}

func (s envfileSchema) withoutVariable(ctx context.Context, parent *core.EnvFile, args struct {
	Name dagql.String
}) (*core.EnvFile, error) {
	return parent.WithoutVariable(args.Name.String()), nil
}

func (s envfileSchema) get(ctx context.Context, parent *core.EnvFile, args struct {
	Name dagql.String
}) (dagql.String, error) {
	if val, ok := parent.Lookup(args.Name.String()); ok {
		return dagql.NewString(val), nil
	}
	return dagql.NewString(""), nil
}

func (s envfileSchema) exists(ctx context.Context, parent *core.EnvFile, args struct {
	Name dagql.String
}) (dagql.Boolean, error) {
	_, ok := parent.Lookup(args.Name.String())
	return dagql.NewBoolean(ok), nil
}

func (s envfileSchema) asFile(ctx context.Context, parent *core.EnvFile, args struct{}) (*core.File, error) {
	return parent.AsFile(ctx)
}

func (s envfileSchema) asEnvFile(ctx context.Context, parent *core.File, args struct {
	Expand dagql.Optional[dagql.Boolean]
}) (*core.EnvFile, error) {
	var expand bool
	if args.Expand.Valid {
		expand = args.Expand.Value.Bool()
	}
	return parent.AsEnvFile(ctx, expand)
}
