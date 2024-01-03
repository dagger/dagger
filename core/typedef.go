package core

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
)

type Function struct {
	// Name is the standardized name of the function (lowerCamelCase), as used for the resolver in the graphql schema
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Args        []*FunctionArg `json:"args"`
	ReturnType  *TypeDef       `json:"returnType"`

	// Below are not in public API

	// OriginalName of the parent object
	ParentOriginalName string `json:"parentOriginalName,omitempty"`

	// The original name of the function as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string `json:"originalName,omitempty"`
}

func NewFunction(name string, returnType *TypeDef) *Function {
	return &Function{
		Name:         strcase.ToLowerCamel(name),
		ReturnType:   returnType,
		OriginalName: name,
	}
}

func (fn *Function) ID() (FunctionID, error) {
	return resourceid.Encode(fn)
}

func (fn *Function) Digest() (digest.Digest, error) {
	return stableDigest(fn)
}

func (fn Function) Clone() *Function {
	cp := fn
	cp.Args = make([]*FunctionArg, len(fn.Args))
	for i, arg := range fn.Args {
		cp.Args[i] = arg.Clone()
	}
	if fn.ReturnType != nil {
		cp.ReturnType = fn.ReturnType.Clone()
	}
	return &cp
}

func (fn *Function) WithDescription(desc string) *Function {
	fn = fn.Clone()
	fn.Description = strings.TrimSpace(desc)
	return fn
}

func (fn *Function) WithArg(name string, typeDef *TypeDef, desc string, defaultValue any) *Function {
	fn = fn.Clone()
	fn.Args = append(fn.Args, &FunctionArg{
		Name:         strcase.ToLowerCamel(name),
		Description:  desc,
		TypeDef:      typeDef,
		DefaultValue: defaultValue,
		OriginalName: name,
	})
	return fn
}

type FunctionArg struct {
	// Name is the standardized name of the argument (lowerCamelCase), as used for the resolver in the graphql schema
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	TypeDef      *TypeDef `json:"typeDef"`
	DefaultValue any      `json:"defaultValue"`

	// Below are not in public API

	// The original name of the argument as provided by the SDK that defined it.
	OriginalName string `json:"originalName,omitempty"`
}

func (arg FunctionArg) Clone() *FunctionArg {
	cp := arg
	cp.TypeDef = arg.TypeDef.Clone()
	// NB(vito): don't bother copying DefaultValue, it's already 'any' so it's
	// hard to imagine anything actually mutating it at runtime vs. replacing it
	// wholesale.
	return &cp
}

func (arg *FunctionArg) ID() (FunctionArgID, error) {
	return resourceid.Encode(arg)
}

type TypeDef struct {
	Kind     TypeDefKind    `json:"kind"`
	Optional bool           `json:"optional"`
	AsList   *ListTypeDef   `json:"asList"`
	AsObject *ObjectTypeDef `json:"asObject"`
}

func (typeDef *TypeDef) ID() (TypeDefID, error) {
	return resourceid.Encode(typeDef)
}

func (typeDef *TypeDef) Digest() (digest.Digest, error) {
	return stableDigest(typeDef)
}

func (typeDef *TypeDef) Underlying() *TypeDef {
	switch typeDef.Kind {
	case TypeDefKindList:
		return typeDef.AsList.ElementTypeDef.Underlying()
	default:
		return typeDef
	}
}

func (typeDef TypeDef) Clone() *TypeDef {
	cp := typeDef
	if typeDef.AsList != nil {
		cp.AsList = typeDef.AsList.Clone()
	}
	if typeDef.AsObject != nil {
		cp.AsObject = typeDef.AsObject.Clone()
	}
	return &cp
}

func (typeDef *TypeDef) WithKind(kind TypeDefKind) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = kind
	return typeDef
}

func (typeDef *TypeDef) WithListOf(elem *TypeDef) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindList)
	typeDef.AsList = &ListTypeDef{
		ElementTypeDef: elem,
	}
	return typeDef
}

func (typeDef *TypeDef) WithObject(name, desc string) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindObject)
	typeDef.AsObject = NewObjectTypeDef(name, desc)
	return typeDef
}

func (typeDef *TypeDef) WithOptional(optional bool) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Optional = optional
	return typeDef
}

func (typeDef *TypeDef) WithObjectField(name string, fieldType *TypeDef, desc string) (*TypeDef, error) {
	if typeDef.AsObject == nil {
		return nil, fmt.Errorf("cannot add function to non-object type: %s", typeDef.Kind)
	}
	typeDef = typeDef.Clone()
	typeDef.AsObject.Fields = append(typeDef.AsObject.Fields, &FieldTypeDef{
		Name:         strcase.ToLowerCamel(name),
		OriginalName: name,
		Description:  desc,
		TypeDef:      fieldType,
	})
	return typeDef, nil
}

func (typeDef *TypeDef) WithObjectFunction(fn *Function) (*TypeDef, error) {
	if typeDef.AsObject == nil {
		return nil, fmt.Errorf("cannot add function to non-object type: %s", typeDef.Kind)
	}
	typeDef = typeDef.Clone()
	fn = fn.Clone()
	fn.ParentOriginalName = typeDef.AsObject.OriginalName
	typeDef.AsObject.Functions = append(typeDef.AsObject.Functions, fn)
	return typeDef, nil
}

func (typeDef *TypeDef) WithObjectConstructor(fn *Function) (*TypeDef, error) {
	if typeDef.AsObject == nil {
		return nil, fmt.Errorf("cannot add constructor function to non-object type: %s", typeDef.Kind)
	}

	typeDef = typeDef.Clone()
	fn = fn.Clone()
	fn.ParentOriginalName = typeDef.AsObject.OriginalName
	typeDef.AsObject.Constructor = fn
	return typeDef, nil
}

type ObjectTypeDef struct {
	// Name is the standardized name of the object (CamelCase), as used for the object in the graphql schema
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Fields      []*FieldTypeDef `json:"fields"`
	Functions   []*Function     `json:"functions"`
	Constructor *Function       `json:"constructor"`
	// SourceModuleName is currently only set when returning the TypeDef from the Objects field on Module
	SourceModuleName string `json:"sourceModuleName"`

	// Below are not in public API

	// The original name of the object as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string `json:"originalName,omitempty"`
}

func NewObjectTypeDef(name, description string) *ObjectTypeDef {
	return &ObjectTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
	}
}

func (typeDef ObjectTypeDef) Clone() *ObjectTypeDef {
	cp := typeDef

	cp.Fields = make([]*FieldTypeDef, len(typeDef.Fields))
	for i, field := range typeDef.Fields {
		cp.Fields[i] = field.Clone()
	}

	cp.Functions = make([]*Function, len(typeDef.Functions))
	for i, fn := range typeDef.Functions {
		cp.Functions[i] = fn.Clone()
	}

	if cp.Constructor != nil {
		cp.Constructor = typeDef.Constructor.Clone()
	}

	return &cp
}

func (typeDef ObjectTypeDef) FieldByName(name string) (*FieldTypeDef, bool) {
	for _, field := range typeDef.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return nil, false
}

func (typeDef ObjectTypeDef) FunctionByName(name string) (*Function, bool) {
	for _, fn := range typeDef.Functions {
		if fn.Name == name {
			return fn, true
		}
	}
	return nil, false
}

type FieldTypeDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TypeDef     *TypeDef `json:"typeDef"`

	// Below are not in public API

	// The original name of the object as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string `json:"originalName,omitempty"`
}

func (typeDef FieldTypeDef) Clone() *FieldTypeDef {
	cp := typeDef
	if typeDef.TypeDef != nil {
		cp.TypeDef = typeDef.TypeDef.Clone()
	}
	return &cp
}

type ListTypeDef struct {
	ElementTypeDef *TypeDef `json:"elementTypeDef"`
}

func (typeDef ListTypeDef) Clone() *ListTypeDef {
	cp := typeDef
	if typeDef.ElementTypeDef != nil {
		cp.ElementTypeDef = typeDef.ElementTypeDef.Clone()
	}
	return &cp
}

type TypeDefKind string

func (k TypeDefKind) String() string {
	return string(k)
}

const (
	TypeDefKindString  TypeDefKind = "StringKind"
	TypeDefKindInteger TypeDefKind = "IntegerKind"
	TypeDefKindBoolean TypeDefKind = "BooleanKind"
	TypeDefKindList    TypeDefKind = "ListKind"
	TypeDefKindObject  TypeDefKind = "ObjectKind"
	TypeDefKindVoid    TypeDefKind = "VoidKind"
)

type FunctionCall struct {
	Name       string       `json:"name"`
	ParentName string       `json:"parentName"`
	Parent     any          `json:"parent"`
	InputArgs  []*CallInput `json:"inputArgs"`
}

func (fnCall *FunctionCall) Digest() (digest.Digest, error) {
	return stableDigest(fnCall)
}

type CallInput struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func (callInput *CallInput) Digest() (digest.Digest, error) {
	return stableDigest(callInput)
}
