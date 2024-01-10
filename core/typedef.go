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

func (fn *Function) IsSubtypeOf(otherFn *Function) bool {
	if fn == nil || otherFn == nil {
		return false
	}

	// check return type
	if !fn.ReturnType.IsSubtypeOf(otherFn.ReturnType) {
		return false
	}

	// check args
	for i, otherFnArg := range otherFn.Args {
		/* TODO: with more effort could probably relax and allow:
		* arg names to not match (only types really matter in theory)
		* mismatches in optional (provided defaults exist, etc.)
		* fewer args in interface fn than object fn (as long as the ones that exist match)
		 */

		if i >= len(fn.Args) {
			return false
		}
		fnArg := fn.Args[i]

		if fnArg.Name != otherFnArg.Name {
			return false
		}

		if fnArg.TypeDef.Optional != otherFnArg.TypeDef.Optional {
			return false
		}

		// We want to be contravariant on arg matching types. So if fnArg asks for a Cat, then
		// we can't invoke it with any Animal since it requested a cat specifically.
		// However, if the fnArg asks for an Animal, we can provide a Cat because that's a subtype of Animal.
		// Thus, we check that the otherFnArg is a subtype of the fnArg (inverse of the covariant matching done
		// on function *return* types above).
		if !otherFnArg.TypeDef.IsSubtypeOf(fnArg.TypeDef) {
			return false
		}
	}

	return true
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
	Kind        TypeDefKind       `json:"kind"`
	Optional    bool              `json:"optional"`
	AsList      *ListTypeDef      `json:"asList"`
	AsObject    *ObjectTypeDef    `json:"asObject"`
	AsInterface *InterfaceTypeDef `json:"asInterface"`
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
	if typeDef.AsInterface != nil {
		cp.AsInterface = typeDef.AsInterface.Clone()
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

func (typeDef *TypeDef) WithInterface(name, desc string) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindInterface)
	typeDef.AsInterface = NewInterfaceTypeDef(name, desc)
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

func (typeDef *TypeDef) WithFunction(fn *Function) (*TypeDef, error) {
	typeDef = typeDef.Clone()
	fn = fn.Clone()
	switch typeDef.Kind {
	case TypeDefKindObject:
		fn.ParentOriginalName = typeDef.AsObject.OriginalName
		typeDef.AsObject.Functions = append(typeDef.AsObject.Functions, fn)
		return typeDef, nil
	case TypeDefKindInterface:
		fn.ParentOriginalName = typeDef.AsInterface.OriginalName
		typeDef.AsInterface.Functions = append(typeDef.AsInterface.Functions, fn)
		return typeDef, nil
	default:
		return nil, fmt.Errorf("cannot add function to type: %s", typeDef.Kind)
	}
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

func (typeDef *TypeDef) IsSubtypeOf(otherDef *TypeDef) bool {
	if typeDef == nil || otherDef == nil {
		return false
	}

	if typeDef.Optional != otherDef.Optional {
		return false
	}

	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindBoolean, TypeDefKindVoid:
		return typeDef.Kind == otherDef.Kind
	case TypeDefKindList:
		if otherDef.Kind != TypeDefKindList {
			return false
		}
		return typeDef.AsList.ElementTypeDef.IsSubtypeOf(otherDef.AsList.ElementTypeDef)
	case TypeDefKindObject:
		switch otherDef.Kind {
		case TypeDefKindObject:
			// For now, assume that if the objects have the same name, they are the same object. This should be a safe assumption
			// within the context of a single, already-namedspace schema, but not safe if objects are compared across schemas
			return typeDef.AsObject.Name == otherDef.AsObject.Name
		case TypeDefKindInterface:
			return typeDef.AsObject.IsSubtypeOf(otherDef.AsInterface)
		default:
			return false
		}
	case TypeDefKindInterface:
		if otherDef.Kind != TypeDefKindInterface {
			return false
		}
		return typeDef.AsInterface.IsSubtypeOf(otherDef.AsInterface)
	default:
		return false
	}
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

func (obj ObjectTypeDef) Clone() *ObjectTypeDef {
	cp := obj

	cp.Fields = make([]*FieldTypeDef, len(obj.Fields))
	for i, field := range obj.Fields {
		cp.Fields[i] = field.Clone()
	}

	cp.Functions = make([]*Function, len(obj.Functions))
	for i, fn := range obj.Functions {
		cp.Functions[i] = fn.Clone()
	}

	if cp.Constructor != nil {
		cp.Constructor = obj.Constructor.Clone()
	}

	return &cp
}

func (obj ObjectTypeDef) FieldByName(name string) (*FieldTypeDef, bool) {
	for _, field := range obj.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return nil, false
}

func (obj ObjectTypeDef) FunctionByName(name string) (*Function, bool) {
	for _, fn := range obj.Functions {
		if fn.Name == name {
			return fn, true
		}
	}
	return nil, false
}

func (obj *ObjectTypeDef) IsSubtypeOf(iface *InterfaceTypeDef) bool {
	if obj == nil || iface == nil {
		return false
	}

	objFnByName := make(map[string]*Function)
	for _, fn := range obj.Functions {
		objFnByName[fn.Name] = fn
	}
	objFieldByName := make(map[string]*FieldTypeDef)
	for _, field := range obj.Fields {
		objFieldByName[field.Name] = field
	}

	for _, ifaceFn := range iface.Functions {
		objFn, objFnExists := objFnByName[ifaceFn.Name]
		objField, objFieldExists := objFieldByName[ifaceFn.Name]

		if !objFnExists && !objFieldExists {
			return false
		}

		if objFieldExists {
			// check return type of field
			return objField.TypeDef.IsSubtypeOf(ifaceFn.ReturnType)
		}

		// otherwise there can only be a match on the objFn
		if ok := objFn.IsSubtypeOf(ifaceFn); !ok {
			return false
		}
	}

	return true
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

type InterfaceTypeDef struct {
	// Name is the standardized name of the interface (CamelCase), as used for the interface in the graphql schema
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Functions   []*Function `json:"functions"`
	// SourceModuleName is currently only set when returning the TypeDef from the Objects field on Module
	SourceModuleName string `json:"sourceModuleName"`

	// Below are not in public API

	// The original name of the interface as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string `json:"originalName,omitempty"`
}

func NewInterfaceTypeDef(name, description string) *InterfaceTypeDef {
	return &InterfaceTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
	}
}

func (iface InterfaceTypeDef) Clone() *InterfaceTypeDef {
	cp := iface

	cp.Functions = make([]*Function, len(iface.Functions))
	for i, fn := range iface.Functions {
		cp.Functions[i] = fn.Clone()
	}

	return &cp
}

func (iface *InterfaceTypeDef) IsSubtypeOf(otherIface *InterfaceTypeDef) bool {
	if iface == nil || otherIface == nil {
		return false
	}

	ifaceFnByName := make(map[string]*Function)
	for _, fn := range iface.Functions {
		ifaceFnByName[fn.Name] = fn
	}

	for _, otherIfaceFn := range otherIface.Functions {
		ifaceFn, ok := ifaceFnByName[otherIfaceFn.Name]
		if !ok {
			return false
		}

		if ok := ifaceFn.IsSubtypeOf(otherIfaceFn); !ok {
			return false
		}
	}

	return true
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
	TypeDefKindString    TypeDefKind = "StringKind"
	TypeDefKindInteger   TypeDefKind = "IntegerKind"
	TypeDefKindBoolean   TypeDefKind = "BooleanKind"
	TypeDefKindList      TypeDefKind = "ListKind"
	TypeDefKindObject    TypeDefKind = "ObjectKind"
	TypeDefKindInterface TypeDefKind = "InterfaceKind"
	TypeDefKindVoid      TypeDefKind = "VoidKind"
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
