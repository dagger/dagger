package templates

import (
	"fmt"
	"go/types"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
)

// A Go type that has been parsed and can be registered with the dagger API
type ParsedType interface {
	// Generated code for registering the type with the dagger API
	TypeDefCode() (*Statement, error)

	// Go type referred to by this type
	GoSubTypes() []types.Type
}

// parseGoTypeReference parses a Go type and returns a TypeSpec for the type *reference* only.
// So if the type is a struct or interface, the returned TypeSpec will not have all the fields,
// only the type name and kind.
func (ps *parseState) parseGoTypeReference(typ types.Type, named *types.Named) (ParsedType, error) {
	switch t := typ.(type) {
	case *types.Named:
		// Named types are any types declared like `type Foo <...>`
		typeSpec, err := ps.parseGoTypeReference(t.Underlying(), t)
		if err != nil {
			return nil, fmt.Errorf("failed to parse named type: %w", err)
		}
		return typeSpec, nil

	case *types.Pointer:
		typeSpec, err := ps.parseGoTypeReference(t.Elem(), named)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pointer type: %w", err)
		}
		return typeSpec, nil

	case *types.Slice:
		elemTypeSpec, err := ps.parseGoTypeReference(t.Elem(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to parse slice element type: %w", err)
		}
		return &parsedSliceType{elemTypeSpec}, nil

	case *types.Basic:
		if t.Kind() == types.Invalid {
			return nil, fmt.Errorf("invalid type: %+v", t)
		}
		return &parsedPrimitiveType{t}, nil

	case *types.Struct:
		if named == nil {
			return nil, fmt.Errorf("struct types must be named")
		}
		typeName := named.Obj().Name()
		if typeName == "" {
			return nil, fmt.Errorf("struct types must be named")
		}
		return &parsedObjectTypeReference{
			name:           typeName,
			referencedType: named,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported type for named type reference %T", t)
	}
}

// parsedPrimitiveType is a parsed type that is a primitive type like string, int, bool, etc.
type parsedPrimitiveType struct {
	goType *types.Basic
}

var _ ParsedType = &parsedPrimitiveType{}

func (spec *parsedPrimitiveType) TypeDefCode() (*Statement, error) {
	var kind Code
	switch spec.goType.Info() {
	case types.IsString:
		kind = Id("Stringkind")
	case types.IsInteger:
		kind = Id("Integerkind")
	case types.IsBoolean:
		kind = Id("Booleankind")
	default:
		return nil, fmt.Errorf("unsupported basic type: %+v", spec.goType)
	}
	return Qual("dag", "TypeDef").Call().Dot("WithKind").Call(
		kind,
	), nil
}

func (spec *parsedPrimitiveType) GoSubTypes() []types.Type {
	return nil
}

// parsedSliceType is a parsed type that is a slice of other types
type parsedSliceType struct {
	underlying ParsedType // the element TypeSpec
}

var _ ParsedType = &parsedSliceType{}

func (spec *parsedSliceType) TypeDefCode() (*Statement, error) {
	underlyingCode, err := spec.underlying.TypeDefCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate underlying type code: %w", err)
	}
	return Qual("dag", "TypeDef").Call().Dot("WithListOf").Call(underlyingCode), nil
}

func (spec *parsedSliceType) GoSubTypes() []types.Type {
	return spec.underlying.GoSubTypes()
}

// parsedObjectTypeReference is a parsed object type that is referred to just by name rather
// than with the full type definition
type parsedObjectTypeReference struct {
	name           string
	referencedType types.Type
}

var _ ParsedType = &parsedObjectTypeReference{}

func (spec *parsedObjectTypeReference) TypeDefCode() (*Statement, error) {
	return Qual("dag", "TypeDef").Call().Dot("WithObject").Call(
		Lit(spec.name),
	), nil
}

func (spec *parsedObjectTypeReference) GoSubTypes() []types.Type {
	return []types.Type{spec.referencedType}
}
