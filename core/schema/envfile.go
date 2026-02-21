package schema

import (
	"context"
	"fmt"

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
		dagql.NodeFunc("withVariable", envFileContentHashWrapper(s.withVariable)).
			WithInput(dagql.CachePerClient).
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
		dagql.NodeFunc("asEnvFile", envFileContentHashWrapper(s.asEnvFile)).
			WithInput(dagql.CachePerClient).
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

type withVariableArgs struct {
	Name  dagql.String
	Value dagql.String

	RawDagOpInternalArgs
}

func (s envfileSchema) withVariable(ctx context.Context, parent dagql.ObjectResult[*core.EnvFile], args withVariableArgs) (*core.EnvFile, error) {
	return parent.Self().WithVariable(args.Name.String(), args.Value.String()), nil
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

type asEnvFileArgs struct {
	Expand dagql.Optional[dagql.Boolean]

	RawDagOpInternalArgs
}

func (s envfileSchema) asEnvFile(ctx context.Context, parent dagql.ObjectResult[*core.File], args asEnvFileArgs) (*core.EnvFile, error) {
	expand := args.Expand.Valid && args.Expand.Value.Bool()
	return parent.Self().AsEnvFile(ctx, expand)
}

func envFileContentHashWrapper[T dagql.Typed, A DagOpInternalArgsIface](
	fn dagql.NodeFuncHandler[T, A, *core.EnvFile],
) dagql.NodeFuncHandler[T, A, dagql.ObjectResult[*core.EnvFile]] {
	return func(ctx context.Context, parent dagql.ObjectResult[T], args A) (inst dagql.ObjectResult[*core.EnvFile], _ error) {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get dagql server: %w", err)
		}

		if args.InDagOp() {
			ef, err := fn(ctx, parent, args)
			if err != nil {
				return inst, err
			}
			return dagql.NewObjectResultForCurrentID(ctx, srv, ef)
		}

		ef, err := DagOp[T, A, *core.EnvFile](ctx, srv, parent, args, fn)
		if err != nil {
			return inst, err
		}

		dop, err := dagql.NewObjectResultForID(ef.Self(), srv, ef.ID())
		if err != nil {
			return inst, err
		}

		dgst, err := ef.Self().Digest(ctx)
		if err != nil {
			return inst, err
		}
		return dop.WithContentDigest(dgst), nil
	}
}
