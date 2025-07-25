package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type environmentSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &environmentSchema{}

func (s environmentSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.FuncWithCacheKey("env", s.environment,
			dagql.CachePerClientSchema[*core.Query, environmentArgs](srv)).
			Doc(`Initialize a new environment`).
			Experimental("Environments are not yet stabilized").
			Args(
				dagql.Arg("privileged").Doc("Give the environment the same privileges as the caller: core API including host access, current module, and dependencies"),
				dagql.Arg("writable").Doc("Allow new outputs to be declared and saved in the environment"),
				dagql.Arg("static").Doc("Instead of a dynamic method set, provide all methods of all inputs as static tools"),
			),
		dagql.FuncWithCacheKey("currentEnv", s.currentEnvironment, dagql.CachePerClient),
	}.Install(srv)
	dagql.Fields[*core.Env]{
		dagql.Func("inputs", s.inputs).
			Doc("return all input values for the environment"),
		dagql.Func("input", s.input).
			Doc("retrieve an input value by name"),
		dagql.Func("outputs", s.outputs).
			Doc("return all output values for the environment"),
		dagql.Func("withoutOutputs", s.withoutOutputs).
			Doc("Return a new environment without any outputs"),
		dagql.Func("output", s.output).
			Doc("retrieve an output value by name"),
		dagql.Func("withHostfs", s.withHostfs).
			Doc("Return a new environment with a new host filesystem").
			Args(
				dagql.Arg("hostfs").Doc("The directory to set as the host filesystem"),
			),
		dagql.Func("withModule", s.withModule).
			Doc("load a module and expose its functions to the model"),
		dagql.Func("withStringInput", s.withStringInput).
			Doc("Create or update an input value of type string").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("value").Doc("The string value to assign to the binding"),
				dagql.Arg("description").Doc("The description of the input"),
			),
		dagql.Func("withStringOutput", s.withStringOutput).
			Doc("Create or update an input value of type string").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("description").Doc("The description of the output"),
			),
	}.Install(srv)
	dagql.Fields[*core.Binding]{
		dagql.Func("name", s.bindingName).
			Doc("The binding name"),
		dagql.Func("typeName", s.bindingTypeName).
			Doc("The binding type"),
		dagql.Func("digest", s.bindingDigest).
			Doc("The digest of the binding value"),
		dagql.Func("asString", s.bindingAsString).
			Doc("The binding's string value"),
		dagql.Func("isNull", s.bindingIsNull).
			Doc("Returns true if the binding is null"),
	}.Install(srv)
	hook := core.EnvHook{Server: srv}
	envObjType, ok := srv.ObjectType(new(core.Env).Type().Name())
	if !ok {
		panic("environment type not found after dagql install")
	}
	hook.ExtendEnvType(envObjType)
	srv.AddInstallHook(hook)
}

type environmentArgs struct {
	Privileged bool `default:"false"`
	Writable   bool `default:"false"`
	Static     bool `default:"false"`
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args environmentArgs) (*core.Env, error) {
	var hostfs dagql.ObjectResult[*core.Directory]
	if mod, err := parent.CurrentModule(ctx); err == nil {
		hostfs = mod.Self().GetSource().ContextDirectory
	} else {
		// FIXME: inherit from somewhere?
		if err := s.srv.Select(ctx, s.srv.Root(), &hostfs, dagql.Selector{
			Field: "directory",
		}); err != nil {
			return nil, err
		}
	}
	deps, err := parent.CurrentServedDeps(ctx)
	if err != nil {
		return nil, err
	}
	env := core.NewEnv(hostfs, deps)
	if args.Privileged {
		env = env.Privileged()
	}
	if args.Writable {
		env = env.Writable()
	}
	if args.Static {
		env = env.Static()
	}
	return env, nil
}

func (s environmentSchema) currentEnvironment(ctx context.Context, parent *core.Query, args struct{}) (res dagql.ObjectResult[*core.Env], _ error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	fc, err := query.CurrentFunctionCall(ctx)
	if err != nil {
		return res, err
	}
	if fc.EnvID == nil {
		return res, fmt.Errorf("no environment found in context")
	}
	return dagql.NewID[*core.Env](fc.EnvID).Load(ctx, s.srv)
}

func (s environmentSchema) inputs(ctx context.Context, env *core.Env, args struct{}) (dagql.Array[*core.Binding], error) {
	return env.Inputs(), nil
}

func (s environmentSchema) input(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Input(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("input not found: %s", args.Name)
}

func (s environmentSchema) output(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Binding, error) {
	b, found := env.Output(args.Name)
	if found {
		return b, nil
	}
	return nil, fmt.Errorf("output not found: %s", args.Name)
}

func (s environmentSchema) outputs(ctx context.Context, env *core.Env, args struct{}) (dagql.Array[*core.Binding], error) {
	return env.Outputs(), nil
}

func (s environmentSchema) withoutOutputs(ctx context.Context, env *core.Env, args struct{}) (*core.Env, error) {
	return env.WithoutOutputs(), nil
}

func (s environmentSchema) withHostfs(ctx context.Context, env *core.Env, args struct {
	Hostfs core.DirectoryID
}) (*core.Env, error) {
	dir, err := args.Hostfs.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return env.WithHostfs(dir), nil
}

func (s environmentSchema) withModule(ctx context.Context, env *core.Env, args struct {
	Module core.ModuleID
}) (*core.Env, error) {
	mod, err := args.Module.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return env.WithModule(mod.Self()), nil
}

func (s environmentSchema) withStringInput(ctx context.Context, env *core.Env, args struct {
	Name        string
	Value       string
	Description string
}) (*core.Env, error) {
	str := dagql.NewString(args.Value)
	return env.WithInput(args.Name, str, args.Description), nil
}

func (s environmentSchema) withStringOutput(ctx context.Context, env *core.Env, args struct {
	Name        string
	Description string
}) (*core.Env, error) {
	return env.WithOutput(args.Name, dagql.String(""), args.Description), nil
}

func (s environmentSchema) bindingName(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Key, nil
}

func (s environmentSchema) bindingTypeName(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.TypeName(), nil
}

func (s environmentSchema) bindingDigest(ctx context.Context, b *core.Binding, args struct{}) (string, error) {
	return b.Digest().String(), nil
}

func (s environmentSchema) bindingAsString(ctx context.Context, b *core.Binding, args struct{}) (dagql.Nullable[dagql.String], error) {
	if str, ok := b.AsString(); ok {
		return dagql.NonNull[dagql.String](dagql.NewString(str)), nil
	}
	return dagql.Null[dagql.String](), nil
}

func (s environmentSchema) bindingIsNull(ctx context.Context, b *core.Binding, args struct{}) (bool, error) {
	return b.Value == nil, nil
}
