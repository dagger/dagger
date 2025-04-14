package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Env struct {
	// Input values
	inputsByName map[string]*Binding
	// Output values
	outputsByName map[string]*Binding
	// Saved objects by ID (Foo#123)
	objsByID map[string]*Binding
	// Auto incrementing number per-type
	typeCount map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
	// An optional root object
	// Can be used to give the environment ambient access to the
	// dagger core API, possibly extended by a module
	root dagql.Object
}

func (*Env) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Env",
		NonNull:   true,
	}
}

func NewEnv() *Env {
	return &Env{
		inputsByName:  map[string]*Binding{},
		outputsByName: map[string]*Binding{},
		objsByID:      map[string]*Binding{},
		typeCount:     map[string]int{},
		idByHash:      map[digest.Digest]string{},
	}
}

// Expose this environment for LLM consumption via MCP.
func (env *Env) MCP(endpoint *LLMEndpoint) *MCP {
	return newMCP(env, endpoint)
}

func (env *Env) Clone() *Env {
	cp := *env
	cp.inputsByName = cloneMap(cp.inputsByName)
	cp.outputsByName = cloneMap(cp.outputsByName)
	cp.objsByID = cloneMap(cp.objsByID)
	cp.typeCount = cloneMap(cp.typeCount)
	cp.idByHash = cloneMap(cp.idByHash)
	return &cp
}

// Set a root object.
// Can be used to give the environment ambient access to
// the dagger core API, possibly extended by a module
func (env *Env) WithRoot(root dagql.Object) *Env {
	env = env.Clone()
	env.root = root
	return env
}

// Return the root object, or nil if no root object is set
func (env *Env) Root() dagql.Object {
	return env.root
}

// Add an input (read-only) binding to the environment
func (env *Env) WithInput(key string, val dagql.Typed, description string) *Env {
	env = env.Clone()
	input := &Binding{Key: key, Value: val, Description: description, env: env}
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
		expectedType: expectedType,
		Description:  description,
		env:          env,
	}
	return env
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

// List all inputs in the environment
func (env *Env) Inputs() []*Binding {
	res := make([]*Binding, 0, len(env.inputsByName))
	for _, v := range env.inputsByName {
		res = append(res, v)
	}
	return res
}

// Lookup an input binding
func (env *Env) Input(key string) (*Binding, bool) {
	// next check for values by ID
	if val, exists := env.objsByID[key]; exists {
		return val, true
	}
	// next check for values by name
	if val, exists := env.inputsByName[key]; exists {
		return val, true
	}
	return nil, false
}

// Remove an input
func (env *Env) WithoutInput(key string) *Env {
	env = env.Clone()
	delete(env.inputsByName, key)
	return env
}

func (env *Env) Ingest(obj dagql.Object, desc string) string {
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
		if desc == "" {
			desc = env.describe(obj.ID())
		}
		env.idByHash[hash] = llmID
		env.objsByID[llmID] = &Binding{
			Key:         llmID,
			Value:       obj,
			Description: desc,
			env:         env,
		}
	}
	return llmID
}

func (env *Env) describe(id *call.ID) string {
	str := new(strings.Builder)
	if recv := id.Receiver(); recv != nil {
		if llmID, ok := env.idByHash[recv.Digest()]; ok {
			str.WriteString(llmID)
		} else {
			str.WriteString(recv.Digest().String())
		}
		str.WriteString(".")
	}
	str.WriteString(id.Field())

	// Include arguments in the description
	if args := id.Args(); len(args) > 0 {
		str.WriteString("(")
		for i, arg := range args {
			if i > 0 {
				str.WriteString(", ")
			}
			str.WriteString(arg.Name())
			str.WriteString(": ")
			str.WriteString(env.displayLit(arg.Value()))
		}
		str.WriteString(")")
	}
	return str.String()
}

func (env *Env) displayLit(lit call.Literal) string {
	switch x := lit.(type) {
	case *call.LiteralID:
		// For ID arguments, try to use LLM IDs
		if llmID, ok := env.idByHash[x.Value().Digest()]; ok {
			return llmID
		} else {
			return x.Value().Type().NamedType()
		}
	case *call.LiteralList:
		list := "["
		_ = x.Range(func(i int, value call.Literal) error {
			if i > 0 {
				list += ","
			}
			list += env.displayLit(value)
			return nil
		})
		list += "]"
		return list
	case *call.LiteralObject:
		obj := "{"
		_ = x.Range(func(i int, name string, value call.Literal) error {
			if i > 0 {
				obj += ","
			}
			obj += name + ": " + env.displayLit(value)
			return nil
		})
		obj += "}"
		return obj
	default:
		return lit.Display()
	}
}

func (env *Env) Types() []string {
	types := make([]string, 0, len(env.typeCount))
	for typ := range env.typeCount {
		types = append(types, typ)
	}
	return types
}

type Binding struct {
	Key         string
	Value       dagql.Typed
	Description string
	env         *Env // TODO: wire this up
	// The expected type
	// Used when defining an output
	expectedType dagql.Type
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
	return b.env.Ingest(obj, b.Description)
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
type EnvHook struct {
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

var TypesHiddenFromEnvExtensions = []dagql.Typed{
	&CurrentModule{},
	&EnumTypeDef{},
	&EnumValueTypeDef{},
	&Env{},
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
			Args: dagql.InputSpecs{
				{
					Name:        "name",
					Description: "The name of the binding",
					Type:        dagql.NewString(""),
				},
				{
					Name:        "value",
					Description: fmt.Sprintf("The %s value to assign to the binding", typeName),
					Type:        idType,
				},
				{
					Name:        "description",
					Description: "The purpose of the input",
					Type:        dagql.NewString(""),
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			env := self.(dagql.Instance[*Env]).Self
			name := args["name"].(dagql.String).String()
			value := args["value"].(dagql.IDType)
			description := args["description"].(dagql.String).String()
			obj, err := s.Server.Load(ctx, value.ID())
			if err != nil {
				return nil, err
			}
			return env.WithInput(name, obj, description), nil
		},
		dagql.CacheSpec{},
	)

	envType.Extend(
		dagql.FieldSpec{
			Name:        "with" + typeName + "Output",
			Description: fmt.Sprintf("Declare a desired %s output to be assigned in the environment", typeName),
			Type:        envType.Typed(),
			Args: dagql.InputSpecs{
				{
					Name:        "name",
					Description: "The name of the binding",
					Type:        dagql.NewString(""),
				},
				{
					Name:        "description",
					Description: "A description of the desired value of the binding",
					Type:        dagql.NewString(""),
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			env := self.(dagql.Instance[*Env]).Self
			name := args["name"].(dagql.String).String()
			desc := args["description"].(dagql.String).String()
			return env.WithOutput(name, targetType, desc), nil
		},
		dagql.CacheSpec{},
	)

	// Install Binding.as<TargetType>()
	bindingType.Extend(
		dagql.FieldSpec{
			Name:        "as" + typeName,
			Description: fmt.Sprintf("Retrieve the binding value, as type %s", typeName),
			Type:        targetType.Typed(),
			Args:        dagql.InputSpecs{},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			binding := self.(dagql.Instance[*Binding]).Self
			val := binding.Value
			if val == nil {
				return nil, fmt.Errorf("binding %q undefined", binding.Key)
			}
			if val.Type().Name() != typeName {
				return nil, fmt.Errorf("binding %q type mismatch: expected %s, got %s", binding.Key, typeName, val.Type())
			}
			return val, nil
		},
		dagql.CacheSpec{
			DoNotCache: "Bindings are mutable",
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
	for _, hiddenType := range TypesHiddenFromModuleSDKs {
		if hiddenType.Type().Name() == typename {
			return
		}
	}
	// skip hardcoded core types that aren't useful as input/output env extensions
	for _, hiddenType := range TypesHiddenFromEnvExtensions {
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
