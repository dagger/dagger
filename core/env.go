package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Env struct {
	// The environment's host filesystem
	Workspace dagql.ObjectResult[*Directory] `field:"true"`

	// The full module dependency chain for the environment, including the core
	// module and any dependencies from the environment's creator
	deps *SchemaBuilder

	// The main module for this environment (the project being worked on)
	MainModule dagql.ObjectResult[*Module]

	// The modules explicitly installed into the environment, to be exposed as
	// tools that implicitly call the constructor with the environment's workspace
	installedModules []dagql.ObjectResult[*Module]

	// Whether the environment exposes toplevel bindings
	privileged bool
	// The env supports declaring new outputs.
	writable bool
}

func (*Env) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Env",
		NonNull:   true,
	}
}

type envKey struct{}

func EnvToContext(ctx context.Context, env dagql.ObjectResult[*Env]) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

func EnvFromContext(ctx context.Context) (dagql.ObjectResult[*Env], bool, error) {
	if env, ok := ctx.Value(envKey{}).(dagql.ObjectResult[*Env]); ok && env.Self() != nil {
		return env, true, nil
	}

	q, _ := CurrentQuery(ctx)
	if q == nil {
		return dagql.ObjectResult[*Env]{}, false, nil
	}
	env, err := q.Server.CurrentEnv(ctx)
	if err != nil {
		return dagql.ObjectResult[*Env]{}, false, err
	}
	if env.Self() == nil {
		return dagql.ObjectResult[*Env]{}, false, nil
	}
	return env, true, nil
}

func NewEnv(workspace dagql.ObjectResult[*Directory], deps *SchemaBuilder) *Env {
	return &Env{
		Workspace: workspace,
		deps:      deps,
	}
}

func (env *Env) Clone() *Env {
	cp := *env
	return &cp
}

func (env *Env) WithWorkspace(dir dagql.ObjectResult[*Directory]) *Env {
	cp := *env
	cp.Workspace = dir
	return &cp
}

func (env *Env) WithMainModule(mod dagql.ObjectResult[*Module]) *Env {
	cp := env.Clone()
	cp.MainModule = mod
	cp.deps = cp.deps.Append(NewUserMod(mod))
	return cp
}

func (env *Env) WithModule(mod dagql.ObjectResult[*Module]) *Env {
	cp := env.Clone()
	cp.deps = cp.deps.Append(NewUserMod(mod))
	cp.installedModules = append(cp.installedModules, mod)
	return cp
}

func (env *Env) Privileged() *Env {
	env = env.Clone()
	env.privileged = true
	return env
}

func (env *Env) IsPrivileged() bool {
	return env.privileged
}

// Return a writable copy of the environment
func (env *Env) Writable() *Env {
	env = env.Clone()
	env.writable = true
	return env
}

// Checks returns a CheckGroup from the main module
func (env *Env) Checks(ctx context.Context, include []string, noGenerate bool) (*CheckGroup, error) {
	if env.MainModule.Self() == nil {
		return nil, fmt.Errorf("no main module set on environment")
	}
	return NewCheckGroup(ctx, env.MainModule, include, noGenerate, false)
}

// Services returns an UpGroup from the main module
func (env *Env) Services(ctx context.Context, include []string) (*UpGroup, error) {
	if env.MainModule.Self() == nil {
		return nil, fmt.Errorf("no main module set on environment")
	}
	return NewUpGroup(ctx, env.MainModule, include)
}

// Check returns a single check by name from the main module
func (env *Env) Check(ctx context.Context, name string) (*Check, error) {
	checkGroup, err := env.Checks(ctx, []string{name}, false)
	if err != nil {
		return nil, err
	}
	switch len(checkGroup.Checks) {
	case 1:
		return checkGroup.Checks[0].Clone(), nil
	case 0:
		return nil, fmt.Errorf("check %q not found", name)
	default:
		return nil, fmt.Errorf("multiple checks found with name %q", name)
	}
}

type Binding struct {
	Key         string
	Value       dagql.Typed
	Description string
	// The expected type
	// Used when defining an output
	ExpectedType string
}

func (*Binding) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Binding",
		NonNull:   true,
	}
}

func (b *Binding) AsObject() (dagql.AnyObjectResult, bool) {
	obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](b.Value)
	return obj, ok
}

// Return the stable object ID for this binding, or an empty string if it's not an object
func (b *Binding) ID() string {
	return b.Key
}

// A Dagql hook for installing module object types into the environment's
// schema.
type EnvHook struct {
	Server *dagql.Server
}

// We don't expose these types to modules SDK codegen, but
// we still want their graphql schemas to be available for
// internal usage. So we use this list to scrub them from
// the introspection JSON that module SDKs use for codegen.
var TypesHiddenFromModuleSDKs = []dagql.Typed{
	&Engine{},
	&EngineCache{},
	&EngineCacheEntry{},
	&EngineCacheEntrySet{},
}

var TypesHiddenFromEnvExtensions = []dagql.Typed{
	&CurrentModule{},
	&EnumTypeDef{},
	&EnumMemberTypeDef{},
	// &Env{},
	// returning an LLM lets agents go completely off the wall and spawn infinite
	// sub-agents
	&LLM{},
	&Error{},
	&ErrorValue{},
	&FieldTypeDef{},
	&FunctionArg{},
	&FunctionCallArgValue{},
	&FunctionCall{},
	&Function{},
	&GeneratedCode{},
	&InputTypeDef{},
	&InterfaceTypeDef{},
	&ListTypeDef{},
	&LLMTokenUsage{},
	&ObjectTypeDef{},
	&ScalarTypeDef{},
	&SDKConfig{},
	&SourceMap{},
	&TerminalLegacy{},
	&TypeDef{},
}

func (s EnvHook) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef dagql.ObjectResult[*TypeDef]) (*Module, error) {
	// Install the target type into the module.
	return mod.WithObject(ctx, targetTypedef)
}
