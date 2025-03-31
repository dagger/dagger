package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Environment struct {
	// Saved objects by prompt var name
	objsByName map[string]*Binding
	// Saved objects by ID (Foo#123)
	objsByID map[string]*Binding
	// Auto incrementing number per-type
	typeCount map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
}

func (*Environment) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Environment",
		NonNull:   true,
	}
}

func NewEnvironment() *Environment {
	return &Environment{
		objsByName: map[string]*Binding{},
		objsByID:   map[string]*Binding{},
		typeCount:  map[string]int{},
		idByHash:   map[digest.Digest]string{},
	}
}

func (env *Environment) Clone() *Environment {
	cp := *env
	cp.objsByName = cloneMap(cp.objsByName)
	cp.objsByID = cloneMap(cp.objsByID)
	cp.typeCount = cloneMap(cp.typeCount)
	cp.idByHash = cloneMap(cp.idByHash)
	return &cp
}

// Add a binding to the environment
func (env *Environment) WithBinding(key string, val dagql.Typed) *Environment {
	env = env.Clone()
	binding := &Binding{Key: key, Value: val, env: env}
	_ = binding.ID() // If val is an object, force its ingestion
	env.objsByName[key] = binding
	return env
}

// List all object bindings in the environment
// TODO: expand from "object bindings" to "all bindings"
func (env *Environment) Bindings() []*Binding {
	res := make([]*Binding, 0, len(env.objsByName))
	for _, v := range env.objsByName {
		res = append(res, v)
	}
	return res
}

// Return all object bindings of the given type
// TODO: expand beyond object types
func (env *Environment) BindingsOfType(typename string) []*Binding {
	res := make([]*Binding, 0, len(env.objsByName))
	for _, v := range env.objsByName {
		if v.TypeName() == typename {
			res = append(res, v)
		}
	}
	return res
}

func (env *Environment) Binding(key string) (*Binding, bool) {
	// next check for values by ID
	if val, exists := env.objsByID[key]; exists {
		return val, true
	}
	// next check for values by name
	if val, exists := env.objsByName[key]; exists {
		return val, true
	}
	return nil, false
}

func (env *Environment) WithoutBinding(key string) *Environment {
	env = env.Clone()
	delete(env.objsByName, key)
	return env
}

func (env *Environment) Ingest(obj dagql.Object) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	typeName := id.Type().NamedType()
	llmID, ok := env.idByHash[hash]
	if !ok {
		env.typeCount[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, env.typeCount[typeName])
		env.idByHash[hash] = llmID
		env.objsByID[llmID] = &Binding{Key: llmID, Value: obj, env: env}
	}
	return llmID
}

func (env *Environment) Types() []string {
	types := make([]string, 0, len(env.typeCount))
	for typ := range env.typeCount {
		types = append(types, typ)
	}
	return types
}

type Binding struct {
	Key   string
	Value dagql.Typed
	env   *Environment // TODO: wire this up
}

func (*Binding) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Binding",
		NonNull:   true,
	}
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

func (b *Binding) AsObject() (dagql.Object, bool) {
	obj, ok := dagql.UnwrapAs[dagql.Object](b.Value)
	return obj, ok
}

func (b *Binding) AsList() (dagql.Enumerable, bool) {
	enum, ok := dagql.UnwrapAs[dagql.Enumerable](b.Value)
	return enum, ok
}

func (b *Binding) TypeName() string {
	if b.Value == nil {
		return Void{}.TypeName()
	}
	return b.Value.Type().Name()
}

// Return the stable object ID for this binding, or an empty string if it's not an object
func (b *Binding) ID() string {
	obj, isObject := b.AsObject()
	if !isObject {
		return ""
	}
	return b.env.Ingest(obj)
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
	return dagql.HashFrom(string(jsonBytes))
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
type EnvironmentHook struct {
	Server *dagql.Server
}

// We don't expose these types to modules SDK codegen, but
// we still want their graphql schemas to be available for
// internal usage. So we use this list to scrub them from
// the introspection JSON that module SDKs use for codegen.
var TypesHiddenFromModuleSDKs = []dagql.Typed{
	&Host{},

	&Engine{},
	&EngineCache{},
	&EngineCacheEntry{},
	&EngineCacheEntrySet{},
}

func (s EnvironmentHook) ExtendEnvironmentType(targetType dagql.ObjectType) error {
	envType, ok := s.Server.ObjectType(new(Environment).Type().Name())
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
	typename := targetType.TypeName()
	// Install get<TargetType>()
	envType.Extend(
		dagql.FieldSpec{
			Name:        "with" + typename + "Binding",
			Description: fmt.Sprintf("Create or update a binding of type %s in the environment", typename),
			Type:        envType.Typed(),
			Args: dagql.InputSpecs{
				{
					Name:        "name",
					Description: "The name of the binding",
					Type:        dagql.NewString(""),
				},
				{
					Name:        "value",
					Description: fmt.Sprintf("The %s value to assign to the binding", typename),
					Type:        idType,
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			env := self.(dagql.Instance[*Environment]).Self
			name := args["name"].(dagql.String).String()
			value := args["value"].(dagql.IDType)
			obj, err := s.Server.Load(ctx, value.ID())
			if err != nil {
				return nil, err
			}
			return env.WithBinding(name, obj), nil
		},
		dagql.CacheSpec{},
	)

	// Install Binding.as<TargetType>()
	bindingType.Extend(
		dagql.FieldSpec{
			Name:        "as" + typename,
			Description: fmt.Sprintf("Retrieve the binding value, as type %s", typename),
			Type:        targetType.Typed(),
			Args:        dagql.InputSpecs{},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			binding := self.(dagql.Instance[*Binding]).Self
			if binding.TypeName() != typename {
				return nil, fmt.Errorf("binding type mismatch: expected %s, got %s", typename, binding.TypeName())
			}
			return binding.Value, nil
		},
		dagql.CacheSpec{},
	)
	return nil
}

func (s EnvironmentHook) InstallObject(targetType dagql.ObjectType) {
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
	for _, hiddenType := range TypesHiddenFromModuleSDKs {
		if hiddenType.Type().Name() == typename {
			return
		}
	}

	if err := s.ExtendEnvironmentType(targetType); err != nil {
		panic(err)
	}
}

func (s EnvironmentHook) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef *TypeDef) (*Module, error) {
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
	if err := s.ExtendEnvironmentType(targetType); err != nil {
		return nil, err
	}
	return mod, nil
}
