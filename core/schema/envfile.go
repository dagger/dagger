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
					Deprecated("Variable expansion is now enabled by default").
					Doc(`Replace "${VAR}" or "$VAR" with the value of other vars`),
			),
	}.Install(srv)

	dagql.Fields[*core.EnvFile]{
		dagql.Func("variables", s.variables).
			Doc("Return all variables").
			Args(
				dagql.Arg("raw").Doc("Return values exactly as written to the file. No quote removal or variable expansion"),
			),
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
				dagql.Arg("raw").Doc("Return the value exactly as written to the file. No quote removal or variable expansion"),
			),
		dagql.Func("exists", s.exists).
			Doc("Check if a variable exists").
			Args(
				dagql.Arg("name").Doc("Variable name"),
			),
		dagql.Func("asFile", s.asFile).
			Doc("Return as a file"),
		dagql.Func("namespace", s.namespace).
			Doc(`Filters variables by prefix and removes the pref from keys.

	Variables without the prefix are excluded. For example, with the prefix
"MY_APP_" and variables:

	MY_APP_TOKEN=topsecret
	MY_APP_NAME=hello
	FOO=bar

 the resulting environment will contain:

	TOKEN=topsecret
	NAME=hello
`).Args(
			dagql.Arg("prefix").Doc(`The prefix to filter by`),
		),
	}.Install(srv)

	dagql.Fields[*core.File]{
		dagql.Func("asEnvFile", s.asEnvFile).
			Args(
				dagql.Arg("expand").
					Doc(`Replace "${VAR}" or "$VAR" with the value of other vars`).
					Deprecated("Variable expansion is now enabled by default"),
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

func (s envfileSchema) variables(ctx context.Context, parent *core.EnvFile, args struct {
	Raw dagql.Optional[dagql.Boolean]
}) (dagql.Array[core.EnvVariable], error) {
	return parent.Variables(ctx, args.Raw.GetOr(dagql.Boolean(false)).Bool())
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
	Raw  dagql.Optional[dagql.Boolean]
}) (dagql.String, error) {
	val, found, err := parent.Lookup(ctx, args.Name.String(), args.Raw.GetOr(dagql.Boolean(false)).Bool())
	if err != nil {
		return dagql.String(""), err
	}
	if found {
		return dagql.String(val), nil
	}
	return dagql.String(""), nil
}

func (s envfileSchema) exists(ctx context.Context, parent *core.EnvFile, args struct {
	Name dagql.String
}) (dagql.Boolean, error) {
	exists := parent.Exists(args.Name.String())
	return dagql.NewBoolean(exists), nil
}

func (s envfileSchema) asFile(ctx context.Context, parent *core.EnvFile, args struct{}) (*core.File, error) {
	return parent.AsFile(ctx)
}

func (s envfileSchema) namespace(ctx context.Context, parent *core.EnvFile, args struct {
	Prefix string
}) (*core.EnvFile, error) {
	return parent.Namespace(ctx, args.Prefix)
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
