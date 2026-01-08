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
			Doc(`Initializes a new environment`).
			Experimental("Environments are not yet stabilized").
			Args(
				dagql.Arg("privileged").Doc("Give the environment the same privileges as the caller: core API including host access, current module, and dependencies"),
				dagql.Arg("writable").Doc("Allow new outputs to be declared and saved in the environment"),
			),
		dagql.FuncWithCacheKey("currentEnv", s.currentEnvironment, dagql.CachePerClient).
			Doc(
				`Returns the current environment`,
				`When called from a function invoked via an LLM tool call, this will be the LLM's current environment, including any modifications made through calling tools. Env values returned by functions become the new environment for subsequent calls, and Changeset values returned by functions are applied to the environment's workspace.`,
				`When called from a module function outside of an LLM, this returns an Env with the current module installed, and with the current module's source directory as its workspace.`,
			).Experimental("Programmatic env access is speculative and might be replaced."),
	}.Install(srv)
	dagql.Fields[*core.Env]{
		dagql.Func("inputs", s.inputs).
			Doc("Returns all input bindings provided to the environment"),
		dagql.Func("input", s.input).
			Doc("Retrieves an input binding by name"),
		dagql.Func("outputs", s.outputs).
			Doc("Returns all declared output bindings for the environment"),
		dagql.Func("withoutOutputs", s.withoutOutputs).
			Doc("Returns a new environment without any outputs"),
		dagql.Func("output", s.output).
			Doc("Retrieves an output binding by name"),
		dagql.Func("withWorkspace", s.withWorkspace).
			Doc("Returns a new environment with the provided workspace").
			Args(
				dagql.Arg("workspace").Doc("The directory to set as the host filesystem"),
			),
		dagql.NodeFuncWithCacheKey("withCurrentModule", s.withCurrentModule, dagql.CachePerClient).
			Doc(
				"Installs the current module into the environment, exposing its functions to the model",
				"Contextual path arguments will be populated using the environment's workspace.",
			),
		dagql.Func("withModule", s.withModule).
			Doc(
				"Installs a module into the environment, exposing its functions to the model",
				"Contextual path arguments will be populated using the environment's workspace.",
			),
		dagql.Func("withStringInput", s.withStringInput).
			Doc("Provides a string input binding to the environment").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("value").Doc("The string value to assign to the binding"),
				dagql.Arg("description").Doc("The description of the input"),
			),
		dagql.Func("withStringOutput", s.withStringOutput).
			Doc("Declares a desired string output binding").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("description").Doc("The description of the output"),
			),
		dagql.Func("checks", s.envChecks).
			Experimental("Checks API is highly experimental and may be removed or replaced entirely.").
			Doc("Return all checks defined by the installed modules").
			Args(
				dagql.Arg("include").Doc("Only include checks matching the specified patterns"),
			),
		dagql.Func("check", s.envCheck).
			Experimental("Checks API is highly experimental and may be removed or replaced entirely.").
			Doc("Return the check with the given name from the installed modules. Must match exactly one check.").
			Args(
				dagql.Arg("name").Doc("The name of the check to retrieve"),
			),
	}.Install(srv)
	dagql.Fields[*core.Binding]{
		dagql.Func("name", s.bindingName).
			Doc("Returns the binding name"),
		dagql.Func("typeName", s.bindingTypeName).
			Doc("Returns the binding type"),
		dagql.Func("digest", s.bindingDigest).
			Doc("Returns the digest of the binding value"),
		dagql.Func("asString", s.bindingAsString).
			Doc("Returns the binding's string value"),
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
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args environmentArgs) (*core.Env, error) {
	var workspace dagql.ObjectResult[*core.Directory]
	if mod, err := parent.CurrentModule(ctx); err == nil {
		workspace = mod.GetSource().ContextDirectory
	} else {
		// FIXME: inherit from somewhere?
		if err := s.srv.Select(ctx, s.srv.Root(), &workspace, dagql.Selector{
			Field: "directory",
		}); err != nil {
			return nil, err
		}
	}
	deps, err := parent.CurrentServedDeps(ctx)
	if err != nil {
		return nil, err
	}
	env := core.NewEnv(workspace, deps)
	if args.Privileged {
		env = env.Privileged()
	}
	if args.Writable {
		env = env.Writable()
	}
	return env, nil
}

func (s environmentSchema) currentEnvironment(ctx context.Context, parent *core.Query, args struct{}) (res dagql.ObjectResult[*core.Env], _ error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	if query.CurrentEnv != nil {
		return dagql.NewID[*core.Env](query.CurrentEnv).Load(ctx, s.srv)
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	mod, err := query.CurrentModule(ctx)
	if err != nil {
		return res, err
	}
	var env dagql.ObjectResult[*core.Env]
	if err := dag.Select(ctx, dag.Root(), &env, dagql.Selector{
		Field: "env",
	}, dagql.Selector{
		Field: "withModule",
		Args: []dagql.NamedInput{
			{
				Name:  "module",
				Value: dagql.NewID[*core.Module](mod.ResultID),
			},
		},
	}, dagql.Selector{
		Field: "withWorkspace",
		Args: []dagql.NamedInput{
			{
				Name: "workspace",
				Value: dagql.NewID[*core.Directory](
					mod.GetSource().ContextDirectory.ID(),
				),
			},
		},
	}); err != nil {
		return res, fmt.Errorf("failed to create env: %w", err)
	}
	return env, nil
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

func (s environmentSchema) withWorkspace(ctx context.Context, env *core.Env, args struct {
	Workspace core.DirectoryID
}) (*core.Env, error) {
	dir, err := args.Workspace.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return env.WithWorkspace(dir), nil
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

func (s environmentSchema) withCurrentModule(ctx context.Context, env dagql.ObjectResult[*core.Env], _ struct{}) (res dagql.ObjectResult[*core.Env], _ error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get current query: %w", err)
	}
	mod, err := query.CurrentModule(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get current module: %w", err)
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get current dagql server: %w", err)
	}
	err = srv.Select(ctx, env, &res, dagql.Selector{
		Field: "withModule",
		Args: []dagql.NamedInput{
			{
				Name:  "module",
				Value: dagql.NewID[*core.Module](mod.ResultID),
			},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to install current module: %w", err)
	}
	return res, nil
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

func (s environmentSchema) envChecks(ctx context.Context, env *core.Env, args struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.CheckGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return env.Checks(ctx, include)
}

func (s environmentSchema) envCheck(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Check, error) {
	return env.Check(ctx, args.Name)
}
