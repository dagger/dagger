package templates

import (
	"fmt"
	"go/types"

	. "github.com/dave/jennifer/jen" //nolint:stylecheck
	"github.com/iancoleman/strcase"
)

// A Go type that has been parsed and can be registered with the dagger API
type ParsedType interface {
	// Generated code for registering the type with the dagger API
	TypeDefCode() (*Statement, error)

	// The underlying go type that ParsedType wraps
	GoType() types.Type

	// Go types referred to by this type
	GoSubTypes() []types.Type
}

type NamedParsedType interface {
	ParsedType
	Name() string
	ModuleName() string
}

func loadFromIDGQLFieldName(spec NamedParsedType) string {
	// NOTE: unfortunately we currently need to account for namespacing here
	return fmt.Sprintf("load%s%sFromID", strcase.ToCamel(spec.ModuleName()), spec.Name())
}

// parseGoTypeReference parses a Go type and returns a TypeSpec for the type *reference* only.
// So if the type is a struct or interface, the returned TypeSpec will not have all the fields,
// only the type name and kind.
// This is so that the typedef can be referenced as the type of an arg, return value or field
// without needing to duplicate the full type definition every time it occurs.
func (ps *parseState) parseGoTypeReference(typ types.Type, named *types.Named, isPtr bool) (ParsedType, error) {
	switch t := typ.(type) {
	case *types.Named:
		// Named types are any types declared like `type Foo <...>`
		typeSpec, err := ps.parseGoTypeReference(t.Underlying(), t, isPtr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse named type: %w", err)
		}
		return typeSpec, nil

	case *types.Pointer:
		typeSpec, err := ps.parseGoTypeReference(t.Elem(), named, true)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pointer type: %w", err)
		}
		return typeSpec, nil

	case *types.Slice:
		elemTypeSpec, err := ps.parseGoTypeReference(t.Elem(), nil, isPtr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse slice element type: %w", err)
		}
		return &parsedSliceType{
			goType:     t,
			underlying: elemTypeSpec,
		}, nil

	case *types.Basic:
		if t.Kind() == types.Invalid {
			return nil, fmt.Errorf("invalid type: %+v", t)
		}
		parsedType := &parsedPrimitiveType{goType: t, isPtr: isPtr}
		if named != nil {
			if enum, _ := ps.parseGoEnum(t, named); enum != nil {
				// type can be parsed as an enum, so let's assume it is
				parsedType.enumType = named
			}

			if ps.isDaggerGenerated(named.Obj()) {
				isEnum := false
				for i := 0; i < named.NumMethods(); i++ {
					method := named.Method(i)
					if method.Name() == "IsEnum" {
						isEnum = true
						break
					}
				}

				if isEnum {
					parsedType.enumType = named
				} else {
					parsedType.scalarType = named
				}
			}

			parsedType.alias = named.Obj().Name()
		}
		return parsedType, nil

	case *types.Struct:
		if named == nil {
			return nil, fmt.Errorf("struct types must be named")
		}
		typeName := named.Obj().Name()
		if typeName == "" {
			return nil, fmt.Errorf("struct types must be named")
		}
		moduleName := ""
		if !ps.isDaggerGenerated(named.Obj()) {
			moduleName = ps.moduleName
		}
		return &parsedObjectTypeReference{
			name:       typeName,
			moduleName: moduleName,
			isPtr:      isPtr,
			goType:     named,
		}, nil

	case *types.Interface:
		if named == nil {
			return nil, fmt.Errorf("interface types must be named")
		}
		typeName := named.Obj().Name()
		if typeName == "" {
			return nil, fmt.Errorf("interface types must be named")
		}
		moduleName := ""
		if !ps.isDaggerGenerated(named.Obj()) {
			moduleName = ps.moduleName
		}
		return &parsedIfaceTypeReference{
			name:       typeName,
			moduleName: moduleName,
			goType:     named,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported type for named type reference %T", t)
	}
}

// parsedPrimitiveType is a parsed type that is a primitive type like string, int, bool, etc.
type parsedPrimitiveType struct {
	goType *types.Basic
	isPtr  bool

	scalarType *types.Named
	enumType   *types.Named

	// if this is something like `type Foo string`, then alias will be "Foo"
	alias string
}

var _ ParsedType = &parsedPrimitiveType{}

func (spec *parsedPrimitiveType) TypeDefCode() (*Statement, error) {
	var kind Code
	switch spec.goType.Info() {
	case types.IsString:
		kind = Id("StringKind")
	case types.IsInteger:
		kind = Id("IntegerKind")
	case types.IsBoolean:
		kind = Id("BooleanKind")
	default:
		return nil, fmt.Errorf("unsupported basic type: %+v", spec.goType)
	}
	var def *Statement
	if spec.scalarType != nil {
		def = Qual("dag", "TypeDef").Call().Dot("WithScalar").Call(Lit(spec.scalarType.Obj().Name()))
	} else if spec.enumType != nil {
		def = Qual("dag", "TypeDef").Call().Dot("WithEnum").Call(Lit(spec.enumType.Obj().Name()))
	} else {
		def = Qual("dag", "TypeDef").Call().Dot("WithKind").Call(kind)
	}
	if spec.isPtr {
		def = def.Dot("WithOptional").Call(Lit(true))
	}
	return def, nil
}

func (spec *parsedPrimitiveType) GoType() types.Type {
	return spec.goType
}

func (spec *parsedPrimitiveType) GoSubTypes() []types.Type {
	subTypes := []types.Type{}
	if spec.scalarType != nil {
		subTypes = append(subTypes, spec.scalarType)
	}
	if spec.enumType != nil {
		subTypes = append(subTypes, spec.enumType)
	}
	return subTypes
}

// parsedSliceType is a parsed type that is a slice of other types
type parsedSliceType struct {
	goType     *types.Slice
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

func (spec *parsedSliceType) GoType() types.Type {
	return spec.goType
}

func (spec *parsedSliceType) GoSubTypes() []types.Type {
	return spec.underlying.GoSubTypes()
}

// parsedObjectTypeReference is a parsed object type that is referred to just by name rather
// than with the full type definition
type parsedObjectTypeReference struct {
	name       string
	moduleName string

	isPtr  bool
	goType types.Type
}

var _ NamedParsedType = &parsedObjectTypeReference{}

func (spec *parsedObjectTypeReference) TypeDefCode() (*Statement, error) {
	return Qual("dag", "TypeDef").Call().Dot("WithObject").Call(
		Lit(spec.name),
	), nil
}

func (spec *parsedObjectTypeReference) GoType() types.Type {
	return spec.goType
}

func (spec *parsedObjectTypeReference) GoSubTypes() []types.Type {
	// because this is a *reference* to a named type, we return the goType itself as a subtype too
	return []types.Type{spec.goType}
}

func (spec *parsedObjectTypeReference) Name() string {
	return spec.name
}

func (spec *parsedObjectTypeReference) ModuleName() string {
	return spec.moduleName
}

// parsedIfaceTypeReference is a parsed object type that is referred to just by name rather
// than with the full type definition
type parsedIfaceTypeReference struct {
	name       string
	moduleName string
	goType     types.Type
}

var _ NamedParsedType = &parsedIfaceTypeReference{}

func (spec *parsedIfaceTypeReference) TypeDefCode() (*Statement, error) {
	return Qual("dag", "TypeDef").Call().Dot("WithInterface").Call(
		Lit(spec.name),
	), nil
}

func (spec *parsedIfaceTypeReference) GoType() types.Type {
	return spec.goType
}

func (spec *parsedIfaceTypeReference) GoSubTypes() []types.Type {
	// because this is a *reference* to a named type, we return the goType itself as a subtype too
	return []types.Type{spec.goType}
}

func (spec *parsedIfaceTypeReference) Name() string {
	return spec.name
}

func (spec *parsedIfaceTypeReference) ModuleName() string {
	return spec.moduleName
}
