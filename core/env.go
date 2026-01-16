package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Env struct {
	// The environment's host filesystem
	Workspace dagql.ObjectResult[*Directory] `field:"true"`

	// The full module dependency chain for the environment, including the core
	// module and any dependencies from the environment's creator
	deps *ModDeps
	// The modules explicitly installed into the environment, to be exposed as
	// tools that implicitly call the constructor with the environment's workspace
	installedModules []*Module

	// Input values
	inputsByName map[string]*Binding
	// Output values
	outputsByName map[string]*Binding
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

func EnvIDToContext(ctx context.Context, env *call.ID) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

func EnvIDFromContext(ctx context.Context) (res *call.ID, ok bool) {
	// Env overidden via explicit context, i.e. from LLM to tool call
	env, ok := ctx.Value(envKey{}).(*call.ID)
	if !ok {
		q, err := CurrentQuery(ctx)
		if err == nil && q.CurrentEnv != nil {
			// Env set on Query, i.e. propagated from LLM to module
			return q.CurrentEnv, true
		}
		return res, false
	}
	return env, true
}

func NewEnv(workspace dagql.ObjectResult[*Directory], deps *ModDeps) *Env {
	return &Env{
		Workspace:     workspace,
		deps:          deps,
		inputsByName:  map[string]*Binding{},
		outputsByName: map[string]*Binding{},
	}
}

func (env *Env) Clone() *Env {
	cp := *env
	cp.inputsByName = maps.Clone(cp.inputsByName)
	cp.outputsByName = maps.Clone(cp.outputsByName)
	for name, bnd := range cp.outputsByName {
		// clone output bindings, since they mutate
		cp.outputsByName[name] = bnd.Clone()
	}
	return &cp
}

func (env *Env) WithWorkspace(dir dagql.ObjectResult[*Directory]) *Env {
	cp := *env
	cp.Workspace = dir
	return &cp
}

func (env *Env) WithModule(mod *Module) *Env {
	cp := env.Clone()
	cp.deps = cp.deps.Append(mod)
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

// Add an input (read-only) binding to the environment
func (env *Env) WithInput(key string, val dagql.Typed, description string) *Env {
	env = env.Clone()
	input := &Binding{Key: key, Value: val, Description: description}
	_ = input.ID() // If val is an object, force its ingestion
	env.inputsByName[key] = input
	return env
}

// Register the desire for a binding in the environment
func (env *Env) WithOutput(key string, expectedType dagql.Type, description string) *Env {
	env = env.Clone()
	env.outputsByName[key] = &Binding{
		Key:          key,
		Value:        nil,
		ExpectedType: expectedType.TypeName(),
		Description:  description,
	}
	return env
}

// Lookup registered outputs in the environment
func (env *Env) Input(key string) (*Binding, bool) {
	if input, exists := env.inputsByName[key]; exists {
		return input, true
	}
	return nil, false
}

// Lookup registered outputs in the environment
func (env *Env) Output(key string) (*Binding, bool) {
	if output, exists := env.outputsByName[key]; exists {
		return output, true
	}
	return nil, false
}

// List all outputs in the environment
func (env *Env) Outputs() []*Binding {
	res := make([]*Binding, 0, len(env.outputsByName))
	for _, v := range env.outputsByName {
		res = append(res, v)
	}
	return res
}

// Remove all outputs from the environment and prevent new ones from being
// declared
func (env *Env) WithoutOutputs() *Env {
	env = env.Clone()
	clear(env.outputsByName)
	env.writable = false
	return env
}

// List all inputs in the environment
func (env *Env) Inputs() []*Binding {
	res := make([]*Binding, 0, len(env.inputsByName))
	for _, v := range env.inputsByName {
		res = append(res, v)
	}
	return res
}

// Remove an input
func (env *Env) WithoutInput(key string) *Env {
	env = env.Clone()
	delete(env.inputsByName, key)
	return env
}

// Checks returns a CheckGroup aggregating all checks from installed modules
func (env *Env) Checks(ctx context.Context, include []string) (*CheckGroup, error) {
	var allChecks []*Check
	for _, mod := range env.installedModules {
		group, err := mod.Checks(ctx, include)
		if err != nil {
			return nil, fmt.Errorf("get checks for module %q: %w", mod.Name(), err)
		}
		allChecks = append(allChecks, group.Checks...)
	}
	return &CheckGroup{
		Module: nil, // nil indicates aggregated group
		Checks: allChecks,
	}, nil
}

// Check returns a single check by name from the installed modules
func (env *Env) Check(ctx context.Context, name string) (*Check, error) {
	checkGroup, err := env.Checks(ctx, []string{name})
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

func (b *Binding) Clone() *Binding {
	cp := *b
	return &cp
}

// Return a string representation of the binding value
func (b *Binding) String() string {
	if b.Value == nil {
		return "null"
	}
	if s, isString := b.AsString(); isString {
		return s
	}
	if _, isObj := b.AsObject(); isObj {
		return b.ID()
	}
	if list, isList := b.AsList(); isList {
		return fmt.Sprintf("%s (length: %d)", b.TypeName(), list.Len())
	}
	return fmt.Sprintf("%q", b.Value)
}

func (b *Binding) AsObject() (dagql.AnyObjectResult, bool) {
	obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](b.Value)
	return obj, ok
}

func (b *Binding) AsList() (dagql.Enumerable, bool) {
	enum, ok := dagql.UnwrapAs[dagql.Enumerable](b.Value)
	return enum, ok
}

func (b *Binding) TypeName() string {
	return b.ExpectedType
}

// Return the stable object ID for this binding, or an empty string if it's not an object
func (b *Binding) ID() string {
	return b.Key
}

// Return a stable digest of the binding's value
func (b *Binding) Digest() digest.Digest {
	obj, isObject := b.AsObject()
	if isObject {
		return obj.ID().Digest()
	}
	jsonBytes, err := json.Marshal(b.Value)
	if err != nil {
		return digest.FromString("")
	}
	return hashutil.HashStrings(string(jsonBytes))
}

func (b *Binding) AsString() (string, bool) {
	s, ok := dagql.UnwrapAs[dagql.String](b.Value)
	if !ok {
		return "", false
	}
	return s.String(), true
}

// A Dagql hook for dynamically extending the Environment and Binding types
// based on available types
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

func (s EnvHook) ExtendEnvType(targetType dagql.ObjectType) error {
	envType, ok := s.Server.ObjectType(new(Env).Type().Name())
	if !ok {
		return fmt.Errorf("failed to lookup environment type")
	}
	bindingType, ok := s.Server.ObjectType(new(Binding).Type().Name())
	if !ok {
		return fmt.Errorf("failed to lookup binding type")
	}
	idType, ok := targetType.IDType()
	if !ok {
		return fmt.Errorf("failed to lookup ID type for %T", targetType)
	}
	typeName := targetType.TypeName()
	// Install get<TargetType>()
	envType.Extend(
		dagql.FieldSpec{
			Name:        "with" + typeName + "Input",
			Description: fmt.Sprintf("Create or update a binding of type %s in the environment", typeName),
			Type:        envType.Typed(),
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{
					Name:        "name",
					Description: "The name of the binding",
					Type:        dagql.NewString(""),
				},
				dagql.InputSpec{
					Name:        "value",
					Description: fmt.Sprintf("The %s value to assign to the binding", typeName),
					Type:        idType,
				},
				dagql.InputSpec{
					Name:        "description",
					Description: "The purpose of the input",
					Type:        dagql.NewString(""),
				},
			),
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			env := self.(dagql.ObjectResult[*Env]).Self()
			name := args["name"].(dagql.String).String()
			value := args["value"].(dagql.IDType)
			description := args["description"].(dagql.String).String()
			obj, err := s.Server.Load(ctx, value.ID())
			if err != nil {
				return nil, err
			}

			return dagql.NewResultForCurrentID(ctx, env.WithInput(name, obj, description))
		},
	)

	envType.Extend(
		dagql.FieldSpec{
			Name:        "with" + typeName + "Output",
			Description: fmt.Sprintf("Declare a desired %s output to be assigned in the environment", typeName),
			Type:        envType.Typed(),
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{
					Name:        "name",
					Description: "The name of the binding",
					Type:        dagql.NewString(""),
				},
				dagql.InputSpec{
					Name:        "description",
					Description: "A description of the desired value of the binding",
					Type:        dagql.NewString(""),
				},
			),
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			env := self.(dagql.ObjectResult[*Env]).Self()
			name := args["name"].(dagql.String).String()
			desc := args["description"].(dagql.String).String()

			return dagql.NewResultForCurrentID(ctx, env.WithOutput(name, targetType, desc))
		},
	)

	// Install Binding.as<TargetType>()
	bindingType.Extend(
		dagql.FieldSpec{
			Name:        "as" + typeName,
			Description: fmt.Sprintf("Retrieve the binding value, as type %s", typeName),
			Type:        targetType.Typed(),
			Args:        dagql.InputSpecs{},
			DoNotCache:  "Bindings are mutable",
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			binding := self.(dagql.ObjectResult[*Binding]).Self()
			val := binding.Value
			if val == nil {
				return nil, fmt.Errorf("binding %q undefined", binding.Key)
			}
			if val.Type().Name() != typeName {
				return nil, fmt.Errorf("binding %q type mismatch: expected %s, got %s", binding.Key, typeName, val.Type())
			}

			res, ok := val.(dagql.AnyResult)
			if !ok {
				var err error
				res, err = dagql.NewResultForCurrentID(ctx, val)
				if err != nil {
					return nil, fmt.Errorf("failed to convert binding %q value to result: %w", binding.Key, err)
				}
			}
			return res, nil
		},
	)
	return nil
}

func (s EnvHook) InstallObject(targetType dagql.ObjectType) {
	typename := targetType.TypeName()
	if strings.HasPrefix(typename, "_") {
		return
	}

	// don't extend LLM for types that we hide from modules, lest the codegen yield a
	// WithEngine(*Engine) that refers to an unknown *Engine type.
	//
	// FIXME: in principle LLM should be able to refer to these types, so this should
	// probably be moved to codegen somehow, i.e. if a field refers to a type that is
	// hidden, don't codegen the field.
	hiddenTypes := make([]dagql.Typed, 0, len(TypesHiddenFromModuleSDKs)+len(TypesHiddenFromEnvExtensions)+1)
	hiddenTypes = append(hiddenTypes, TypesHiddenFromModuleSDKs...)
	hiddenTypes = append(hiddenTypes, TypesHiddenFromEnvExtensions...)
	hiddenTypes = append(hiddenTypes, &Host{})
	for _, hiddenType := range hiddenTypes {
		if hiddenType.Type().Name() == typename {
			return
		}
	}

	if err := s.ExtendEnvType(targetType); err != nil {
		panic(err)
	}
}

func (s EnvHook) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef *TypeDef) (*Module, error) {
	// Install the target type
	mod, err := mod.WithObject(ctx, targetTypedef)
	if err != nil {
		return nil, err
	}
	typename := targetTypedef.Type().Name()
	targetType, ok := s.Server.ObjectType(typename)
	if !ok {
		return nil, fmt.Errorf("can't retrieve object type %s", typename)
	}
	if err := s.ExtendEnvType(targetType); err != nil {
		return nil, err
	}
	return mod, nil
}
