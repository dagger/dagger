package core

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
)

type Environment struct {
	// Saved objects by prompt var name
	objsByName map[string]*Binding
	// Saved objects by ID (Foo#123)
	objsByID map[string]*Binding
	// String variables assigned to the environment
	varsByName map[string]*Binding
	// Auto incrementing number per-type
	typeCount map[string]int
	// The LLM-friendly ID ("Container#123") for each object
	idByHash map[digest.Digest]string
}

func NewEnvironment() *Environment {
	return &Environment{
		objsByName: map[string]*Binding{},
		objsByID:   map[string]*Binding{},
		varsByName: map[string]*Binding{},
		typeCount:  map[string]int{},
		idByHash:   map[digest.Digest]string{},
	}
}

func (env *Environment) Clone() *Environment {
	cp := *env
	cp.objsByName = cloneMap(cp.objsByName)
	cp.objsByID = cloneMap(cp.objsByID)
	cp.varsByName = cloneMap(cp.varsByName)
	cp.typeCount = cloneMap(cp.typeCount)
	cp.idByHash = cloneMap(cp.idByHash)
	return &cp
}

// Add a binding to the environment
func (env *Environment) WithBinding(key string, obj dagql.Object) *Environment {
	env = env.Clone()
	env.objsByName[key] = &Binding{Key: key, Value: obj}
	env.Ingest(obj)
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

// Add a string variable to the environment
// TODO: merge into bindings
func (env *Environment) WithVariable(name, value string) *Environment {
	env = env.Clone()
	env.varsByName[name] = &Binding{Key: name, Value: dagql.NewString(value), env: env}
	return env
}

// Retrieve a string variable
// TODO: merge into bindings
func (env *Environment) Variable(name string) (*Binding, bool) {
	b, found := env.varsByName[name]
	return b, found
}

// List all variables
// TODO: merge into bindings
func (env *Environment) Variables() []*Binding {
	res := make([]*Binding, 0, len(env.varsByName))
	for _, v := range env.varsByName {
		res = append(res, v)
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

func (b *Binding) AsObject() (dagql.Object, bool) {
	obj, ok := dagql.UnwrapAs[dagql.Object](b.Value)
	return obj, ok
}

func (b *Binding) AsEnumerable() (dagql.Enumerable, bool) {
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
