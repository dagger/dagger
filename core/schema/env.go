package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type environmentSchema struct {
}

var _ SchemaResolvers = &environmentSchema{}

func (s environmentSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("env", s.environment).
			WithInput(dagql.PerClientInput, dagql.CurrentSchemaInput).
			Doc(`Initializes a new environment`).
			Experimental("Environments are not yet stabilized").
			Args(
				dagql.Arg("privileged").Doc("Give the environment the same privileges as the caller: core API including host access, current module, and dependencies"),
				dagql.Arg("writable").Doc("Allow new outputs to be declared and saved in the environment"),
			),
		dagql.Func("currentEnv", s.currentEnvironment).
			WithInput(dagql.PerClientInput).
			Doc(
				`Returns the current environment`,
				`When called from a function invoked via an LLM tool call, this will be the LLM's current environment, including any modifications made through calling tools. Env values returned by functions become the new environment for subsequent calls, and Changeset values returned by functions are applied to the environment's workspace.`,
				`When called from a module function outside of an LLM, this returns an Env with the current module installed, and with the current module's source directory as its workspace.`,
			).Experimental("Programmatic env access is speculative and might be replaced."),
	}.Install(srv)
	dagql.Fields[*core.Env]{
		dagql.Func("withWorkspace", s.withWorkspace).
			Doc("Returns a new environment with the provided workspace").
			Args(
				dagql.Arg("workspace").Doc("The directory to set as the host filesystem"),
			),
		dagql.NodeFunc("withCurrentModule", s.withCurrentModule).
			WithInput(dagql.PerClientInput).
			Doc(
				"Installs the current module into the environment, exposing its functions to the model",
				"Contextual path arguments will be populated using the environment's workspace.",
			),
		dagql.Func("withMainModule", s.withMainModule).
			Doc(
				"Sets the main module for this environment (the project being worked on)",
				"Contextual path arguments will be populated using the environment's workspace.",
			),
		dagql.Func("withModule", s.withModule).
			Doc(
				"Installs a module into the environment, exposing its functions to the model",
				"Contextual path arguments will be populated using the environment's workspace.",
			).
			Deprecated("Use withMainModule instead"),
		dagql.Func("checks", s.envChecks).
			Experimental("Checks API is highly experimental and may be removed or replaced entirely.").
			Doc("Return all checks defined by the installed modules").
			Args(
				dagql.Arg("include").Doc("Only include checks matching the specified patterns"),
				dagql.Arg("noGenerate").Doc("When true, only return annotated check functions; exclude generate-as-checks").
					View(AfterVersion("v0.21.0")),
			),
		dagql.Func("check", s.envCheck).
			Experimental("Checks API is highly experimental and may be removed or replaced entirely.").
			Doc("Return the check with the given name from the installed modules. Must match exactly one check.").
			Args(
				dagql.Arg("name").Doc("The name of the check to retrieve"),
			),
		dagql.Func("services", s.envServices).
			Experimental("Services API is highly experimental and may be removed or replaced entirely.").
			Doc("Return all services defined by the installed modules").
			Args(
				dagql.Arg("include").Doc("Only include services matching the specified patterns"),
			),
	}.Install(srv)
}

type environmentArgs struct {
	Privileged bool `default:"false"`
	Writable   bool `default:"false"`
}

func (s environmentSchema) environment(ctx context.Context, parent *core.Query, args environmentArgs) (*core.Env, error) {
	var workspace dagql.ObjectResult[*core.Directory]
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	if mod, err := parent.CurrentModule(ctx); err == nil {
		workspace = mod.Self().GetSource().ContextDirectory
	} else {
		// FIXME: inherit from somewhere?
		if err := dag.Select(ctx, dag.Root(), &workspace, dagql.Selector{
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
	currentEnv, err := parent.CurrentEnv(ctx)
	if err != nil {
		return res, err
	}
	if currentEnv.Self() != nil {
		return currentEnv, nil
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	mod, err := query.CurrentModule(ctx)
	if err != nil {
		return res, err
	}
	modID, err := mod.ID()
	if err != nil {
		return res, fmt.Errorf("get current module ID: %w", err)
	}
	workspaceID, err := mod.Self().GetSource().ContextDirectory.ID()
	if err != nil {
		return res, fmt.Errorf("get current module workspace ID: %w", err)
	}
	var env dagql.ObjectResult[*core.Env]
	if err := dag.Select(ctx, dag.Root(), &env, dagql.Selector{
		Field: "env",
	}, dagql.Selector{
		Field: "withMainModule",
		Args: []dagql.NamedInput{
			{
				Name:  "module",
				Value: dagql.NewID[*core.Module](modID),
			},
		},
	}, dagql.Selector{
		Field: "withWorkspace",
		Args: []dagql.NamedInput{
			{
				Name:  "workspace",
				Value: dagql.NewID[*core.Directory](workspaceID),
			},
		},
	}); err != nil {
		return res, fmt.Errorf("failed to create env: %w", err)
	}
	return env, nil
}

func (s environmentSchema) withWorkspace(ctx context.Context, env *core.Env, args struct {
	Workspace core.DirectoryID
}) (*core.Env, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := args.Workspace.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return env.WithWorkspace(dir), nil
}

func (s environmentSchema) withMainModule(ctx context.Context, env *core.Env, args struct {
	Module core.ModuleID
}) (*core.Env, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	mod, err := args.Module.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return env.WithMainModule(mod), nil
}

func (s environmentSchema) withModule(ctx context.Context, env *core.Env, args struct {
	Module core.ModuleID
}) (*core.Env, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	mod, err := args.Module.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return env.WithModule(mod), nil
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
	modID, err := mod.ID()
	if err != nil {
		return res, fmt.Errorf("get current module ID: %w", err)
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
				Value: dagql.NewID[*core.Module](modID),
			},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to install current module: %w", err)
	}
	return res, nil
}

func (s environmentSchema) envChecks(ctx context.Context, env *core.Env, args struct {
	Include    dagql.Optional[dagql.ArrayInput[dagql.String]]
	NoGenerate dagql.Optional[dagql.Boolean]
}) (*core.CheckGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return env.Checks(ctx, include, args.NoGenerate.GetOr(false).Bool())
}

func (s environmentSchema) envCheck(ctx context.Context, env *core.Env, args struct {
	Name string
}) (*core.Check, error) {
	return env.Check(ctx, args.Name)
}

func (s environmentSchema) envServices(ctx context.Context, env *core.Env, args struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.UpGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return env.Services(ctx, include)
}
