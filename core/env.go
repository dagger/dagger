package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Env struct {
	// The server providing the environment's types + schema
	srv *dagql.Server
	// Input values
	inputsByName map[string]*Binding
	// Output values
	outputsByName map[string]*Binding
	// Saved objects by ID (Foo#123)
	objsByID map[string]*Binding
	// Auto incrementing number per-type
	typeCounts map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
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

func NewEnv(srv *dagql.Server) *Env {
	return &Env{
		srv:           srv,
		inputsByName:  map[string]*Binding{},
		outputsByName: map[string]*Binding{},
		objsByID:      map[string]*Binding{},
		typeCounts:    map[string]int{},
		idByHash:      map[digest.Digest]string{},
	}
}

// Expose this environment for LLM consumption via MCP.
func (env *Env) MCP() *MCP {
	return newMCP(env)
}

func (env *Env) Clone() *Env {
	cp := *env
	cp.inputsByName = maps.Clone(cp.inputsByName)
	cp.outputsByName = maps.Clone(cp.outputsByName)
	cp.objsByID = maps.Clone(cp.objsByID)
	cp.typeCounts = maps.Clone(cp.typeCounts)
	cp.idByHash = maps.Clone(cp.idByHash)
	for name, bnd := range cp.outputsByName {
		// clone output bindings, since they mutate
		cp.outputsByName[name] = bnd.Clone()
	}
	return &cp
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

// Declare an output binding in the environment, which must be writable.
func (env *Env) DeclareOutput(name string, typ dagql.Type, description string) error {
	if env.writable {
		env.outputsByName[name] = &Binding{
			Key:          name,
			Value:        nil,
			ExpectedType: typ.TypeName(),
			Description:  description,
			env:          env,
		}
		return nil
	}
	return errors.New("environment is not writable")
}

// Add an input (read-only) binding to the environment
func (env *Env) WithInput(key string, val dagql.Typed, description string) *Env {
	env = env.Clone()
	input := &Binding{Key: key, Value: val, Description: description, env: env}
	_ = input.ID() // If val is an object, force its ingestion
	env.inputsByName[key] = input
	return env
}

// Add an input (read-only) binding to the environment
func (env *Env) WithBinding(name string, object dagql.AnyResult, description string) *Env {
	env = env.Clone()
	binding := &Binding{Key: name, Value: object, Description: description, env: env}
	_ = binding.ID() // If val is an object, force its ingestion
	// FIXME: rename 'inputs' to 'bindings' internally
	env.inputsByName[name] = binding
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
	return env.Bindings()
}

// List all bindings
func (env *Env) Bindings() []*Binding {
	res := make([]*Binding, 0, len(env.inputsByName))
	for _, v := range env.inputsByName {
		res = append(res, v)
	}
	return res
}

// Lookup an input binding
func (env *Env) Input(key string) (*Binding, bool) {
	return env.Binding(key)
}

// Lookup a binding
func (env *Env) Binding(name string) (*Binding, bool) {
	// next check for values by ID
	if val, exists := env.objsByID[name]; exists {
		return val, true
	}
	// next check for values by name
	if val, exists := env.inputsByName[name]; exists {
		return val, true
	}
	return nil, false
}

// Remove an input
func (env *Env) WithoutBinding(name string) *Env {
	env = env.Clone()
	delete(env.inputsByName, name)
	return env
}

// Remove an input
func (env *Env) WithoutInput(key string) *Env {
	return env.WithoutBinding(key)
}

func (env *Env) Ingest(obj dagql.AnyResult, desc string) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	typeName := id.Type().NamedType()
	llmID, ok := env.idByHash[hash]
	if !ok {
		env.typeCounts[typeName]++
		llmID = fmt.Sprintf("%s#%d", typeName, env.typeCounts[typeName])
		if desc == "" {
			desc = env.describe(obj.ID())
		}
		env.idByHash[hash] = llmID
		env.objsByID[llmID] = &Binding{
			Key:          llmID,
			Value:        obj,
			Description:  desc,
			ExpectedType: obj.Type().Name(),
			env:          env,
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
	types := make([]string, 0, len(env.typeCounts))
	for typ := range env.typeCounts {
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
	&Engine{},
	&EngineCache{},
	&EngineCacheEntry{},
	&EngineCacheEntrySet{},
}

var TypesHiddenFromEnvExtensions = []dagql.Typed{
	&CurrentModule{},
	&EnumTypeDef{},
	&EnumMemberTypeDef{},
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
		dagql.CacheSpec{},
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
