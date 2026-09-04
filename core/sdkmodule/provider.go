// Package sdkmodule loads and calls modules that implement the SDK-module
// authoring interface.
//
// This package is intentionally separate from core/sdk. The latter loads the
// runtime and code-generation implementation named by dagger-module.toml. An
// SDK module is a normal installed workspace module that edits a Workspace.
package sdkmodule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
)

const (
	findClientRootFunction    = "findClientRoot"
	generateScopeFunction     = "generateScope"
	defaultModulePathFunction = "defaultModulePath"
)

// Provider is one loaded SDK module.
type Provider struct {
	root  *core.Query
	mod   dagql.ObjectResult[*core.Module]
	funcs map[string]*core.Function
}

// Implements reports whether mod implements the complete SDK-module interface.
// It performs schema validation only. Load performs the same validation and
// also constructs the provider instance.
func Implements(mod *core.Module) bool {
	if mod == nil {
		return false
	}
	provider := &Provider{funcs: implementedFunctions(mod)}
	return provider.validate() == nil
}

// Module returns the loaded provider module result.
func (provider *Provider) Module() dagql.ObjectResult[*core.Module] {
	return provider.mod
}

// Load resolves ref as a normal module and validates the SDK-module interface.
// parentSrc supplies the workspace filesystem context for local provider refs.
func Load(
	ctx context.Context,
	root *core.Query,
	ref string,
	parentSrc *core.ModuleSource,
	settings map[string]any,
) (*Provider, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("SDK module reference is required")
	}

	bk, err := root.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("get engine client for SDK module %q: %w", ref, err)
	}
	dag, err := root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("get DagQL server for SDK module %q: %w", ref, err)
	}

	src, err := core.ResolveDepToSource(ctx, bk, dag, parentSrc, ref, "", "")
	if err != nil {
		return nil, fmt.Errorf("resolve SDK module %q: %w", ref, err)
	}
	if !src.Self().ConfigExists {
		return nil, fmt.Errorf("SDK module source %q has no module configuration", ref)
	}

	selector := dagql.Selector{
		Field: "asModule",
		Args: []dagql.NamedInput{
			{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
		},
	}
	if len(settings) > 0 {
		raw, err := json.Marshal(settings)
		if err != nil {
			return nil, fmt.Errorf("encode SDK module settings: %w", err)
		}
		selector.Args = append(selector.Args, dagql.NamedInput{
			Name:  "legacyWorkspaceConfigJson",
			Value: dagql.String(raw),
		})
	}

	var mod dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, src, &mod, selector); err != nil {
		return nil, fmt.Errorf("load SDK module %q: %w", ref, err)
	}

	provider := &Provider{
		root:  root,
		mod:   mod,
		funcs: implementedFunctions(mod.Self()),
	}
	if err := provider.validate(); err != nil {
		return nil, fmt.Errorf("SDK module %q: %w", ref, err)
	}
	if _, err := provider.instantiate(ctx); err != nil {
		return nil, err
	}
	return provider, nil
}

// FindClientRoot finds the SDK client root that contains ws.Cwd. An empty
// result means this provider found no usable root.
func (provider *Provider) FindClientRoot(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
) (string, error) {
	inst, err := provider.instantiate(ctx)
	if err != nil {
		return "", err
	}
	wsID, err := ws.ID()
	if err != nil {
		return "", fmt.Errorf("SDK module findClientRoot workspace ID: %w", err)
	}
	var result dagql.String
	if err := inst.dag.Select(ctx, inst.object, &result, dagql.Selector{
		Field: findClientRootFunction,
		Args: []dagql.NamedInput{
			{Name: "ws", Value: dagql.NewID[*core.Workspace](wsID)},
		},
	}); err != nil {
		return "", fmt.Errorf("call SDK module findClientRoot: %w", err)
	}
	return result.String(), nil
}

// ImplementsDefaultModulePath reports whether this provider implements the
// optional defaultModulePath function.
func (provider *Provider) ImplementsDefaultModulePath() bool {
	return provider.funcs[defaultModulePathFunction] != nil
}

// DefaultModulePath asks the provider where a new module named name belongs.
// An empty result means the engine picks the path. Callers must skip this when
// the user passed an explicit path.
func (provider *Provider) DefaultModulePath(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	name string,
) (string, error) {
	if !provider.ImplementsDefaultModulePath() {
		return "", nil
	}
	inst, err := provider.instantiate(ctx)
	if err != nil {
		return "", err
	}
	wsID, err := ws.ID()
	if err != nil {
		return "", fmt.Errorf("SDK module defaultModulePath workspace ID: %w", err)
	}
	var result dagql.String
	if err := inst.dag.Select(ctx, inst.object, &result, dagql.Selector{
		Field: defaultModulePathFunction,
		Args: []dagql.NamedInput{
			{Name: "ws", Value: dagql.NewID[*core.Workspace](wsID)},
			{Name: "name", Value: dagql.String(name)},
		},
	}); err != nil {
		return "", fmt.Errorf("call SDK module defaultModulePath: %w", err)
	}
	return result.String(), nil
}

// GenerateScope reconciles all SDK-managed state for ws.Cwd.
func (provider *Provider) GenerateScope(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	isModule bool,
	name string,
	clients []dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Workspace], error) {
	clientIDs := make(dagql.ArrayInput[dagql.ID[*core.ModuleSource]], len(clients))
	for i, client := range clients {
		clientID, err := client.ID()
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK module client target %d ID: %w", i, err)
		}
		clientIDs[i] = dagql.NewID[*core.ModuleSource](clientID)
	}
	return provider.callWorkspaceFunction(ctx, generateScopeFunction, ws, []dagql.NamedInput{
		{Name: "isModule", Value: dagql.Boolean(isModule)},
		{Name: "name", Value: dagql.String(name)},
		{Name: "clients", Value: clientIDs},
	})
}

type instance struct {
	dag    *dagql.Server
	object dagql.AnyObjectResult
}

func (provider *Provider) instantiate(ctx context.Context) (*instance, error) {
	dag, err := dagql.NewServer(ctx, provider.root)
	if err != nil {
		return nil, fmt.Errorf("create SDK-module server: %w", err)
	}
	dag.Around(core.AroundFunc)
	core.InstallCoreSchemaLoaders(dag)

	if err := core.NewUserMod(provider.mod).Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("install SDK module %q: %w", provider.mod.Self().Name(), err)
	}
	defaultDeps, err := provider.root.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("get default dependencies for SDK module %q: %w", provider.mod.Self().Name(), err)
	}
	for _, dep := range defaultDeps.Mods() {
		if err := dep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("install default dependency %q for SDK module %q: %w", dep.Name(), provider.mod.Self().Name(), err)
		}
	}

	var object dagql.AnyObjectResult
	if err := dag.Select(ctx, dag.Root(), &object, dagql.Selector{
		Field: strcase.ToLowerCamel(provider.mod.Self().Name()),
	}); err != nil {
		return nil, fmt.Errorf("construct SDK module %q: %w", provider.mod.Self().Name(), err)
	}
	return &instance{dag: dag, object: object}, nil
}

func (provider *Provider) callWorkspaceFunction(
	ctx context.Context,
	function string,
	ws dagql.ObjectResult[*core.Workspace],
	extra []dagql.NamedInput,
) (dagql.ObjectResult[*core.Workspace], error) {
	inst, err := provider.instantiate(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	wsID, err := ws.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK module %s workspace ID: %w", function, err)
	}
	args := []dagql.NamedInput{{Name: "ws", Value: dagql.NewID[*core.Workspace](wsID)}}
	args = append(args, extra...)

	var result dagql.ObjectResult[*core.Workspace]
	if err := inst.dag.Select(ctx, inst.object, &result, dagql.Selector{
		Field: function,
		Args:  args,
	}); err != nil {
		return result, fmt.Errorf("call SDK module %s: %w", function, err)
	}
	return result, nil
}

func (provider *Provider) validate() error {
	checks := []struct {
		name     string
		result   string
		args     []argumentShape
		optional bool
	}{
		{
			name:   findClientRootFunction,
			result: "String!",
			args:   []argumentShape{{name: "ws", typ: "Workspace!"}},
		},
		{
			name:   generateScopeFunction,
			result: "Workspace!",
			args: []argumentShape{
				{name: "ws", typ: "Workspace!"},
				{name: "isModule", typ: "Boolean!"},
				{name: "name", typ: "String!"},
				{name: "clients", typ: "[ModuleSource!]!"},
			},
		},
		{
			name:   defaultModulePathFunction,
			result: "String!",
			args: []argumentShape{
				{name: "ws", typ: "Workspace!"},
				{name: "name", typ: "String!"},
			},
			optional: true,
		},
	}

	for _, check := range checks {
		fn := provider.funcs[check.name]
		if fn == nil {
			if check.optional {
				continue
			}
			return fmt.Errorf("required function %s is not implemented", check.name)
		}
		if got := fn.ReturnType.Self().ToType().String(); got != check.result {
			return fmt.Errorf("%s must return %s, not %s", check.name, check.result, got)
		}
		if len(fn.Args) != len(check.args) {
			return fmt.Errorf("%s must have %d arguments, not %d", check.name, len(check.args), len(fn.Args))
		}
		for i, expected := range check.args {
			arg := fn.Args[i].Self()
			if arg.Name != expected.name {
				return fmt.Errorf("%s argument %d must be named %s, not %s", check.name, i+1, expected.name, arg.Name)
			}
			if got := arg.TypeDef.Self().ToType().String(); got != expected.typ {
				return fmt.Errorf("%s argument %s must have type %s, not %s", check.name, expected.name, expected.typ, got)
			}
		}
	}
	return nil
}

type argumentShape struct {
	name string
	typ  string
}

func implementedFunctions(mod *core.Module) map[string]*core.Function {
	functions := map[string]*core.Function{}
	main, ok := mod.MainObject()
	if !ok {
		return functions
	}
	for _, fn := range main.Functions {
		name := strcase.ToLowerCamel(fn.Self().Name)
		switch name {
		case findClientRootFunction, generateScopeFunction, defaultModulePathFunction:
			functions[name] = fn.Self()
		}
	}
	return functions
}
