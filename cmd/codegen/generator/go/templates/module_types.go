package templates

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"

	"github.com/dagger/dagger/core"
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

	// TypeDef representation of the type
	TypeDefObject() (*core.TypeDef, error)
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

func typeName(spec NamedParsedType) string {
	if spec.ModuleName() == "" {
		return fmt.Sprintf("dagger.%s", spec.Name())
	}
	return spec.Name()
}

// parseGoTypeReference parses a Go type and returns a TypeSpec for the type *reference* only.
// So if the type is a struct or interface, the returned TypeSpec will not have all the fields,
// only the type name and kind.
// This is so that the typedef can be referenced as the type of an arg, return value or field
// without needing to duplicate the full type definition every time it occurs.
func (ps *parseState) parseGoTypeReference(typ types.Type, named *types.Named, isPtr bool) (ParsedType, error) {
	switch t := typ.(type) {
	case *types.Alias:
		typeSpec, err := ps.parseGoTypeReference(t.Rhs(), nil, isPtr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse alias type: %w", err)
		}
		return typeSpec, nil

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
		enumType, err := ps.parseGoEnumReference(t, named, isPtr)
		if err != nil {
			return nil, err
		}
		if enumType != nil {
			// type can be parsed as an enum, so let's assume it is
			return enumType, nil
		}

		parsedType := &parsedPrimitiveType{goType: t, isPtr: isPtr}
		if named != nil {
			parsedType.alias = named.Obj().Name()
			if ps.isDaggerGenerated(named.Obj()) {
				parsedType.scalarType = named
			} else {
				parsedType.moduleName = ps.moduleName
			}
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
	goType     *types.Basic
	isPtr      bool
	moduleName string

	scalarType *types.Named

	// if this is something like `type Foo string`, then alias will be "Foo"
	alias string
}

var _ ParsedType = &parsedPrimitiveType{}

func (spec *parsedPrimitiveType) TypeDefCode() (*Statement, error) {
	var kind Code
	if spec.goType.Kind() == types.Invalid {
		// NOTE: this is odd, but it doesn't matter, because the module won't
		// pass the compilation step if there are invalid types - we just want
		// to not error out horribly in codegen
		kind = Id("dagger").Dot("TypeDefKindVoidKind")
	} else {
		switch spec.goType.Info() {
		case types.IsString:
			kind = Id("dagger").Dot("TypeDefKindStringKind")
		case types.IsInteger:
			kind = Id("dagger").Dot("TypeDefKindIntegerKind")
		case types.IsBoolean:
			kind = Id("dagger").Dot("TypeDefKindBooleanKind")
		case types.IsFloat:
			kind = Id("dagger").Dot("TypeDefKindFloatKind")
		default:
			return nil, fmt.Errorf("unsupported basic type: %+v", spec.goType)
		}
	}
	var def *Statement
	if spec.scalarType != nil {
		def = Qual("dag", "TypeDef").Call().Dot("WithScalar").Call(Lit(spec.scalarType.Obj().Name()))
	} else {
		def = Qual("dag", "TypeDef").Call().Dot("WithKind").Call(kind)
	}
	if spec.isPtr {
		def = def.Dot("WithOptional").Call(Lit(true))
	}
	return def, nil
}

func (spec *parsedPrimitiveType) TypeDefObject() (*core.TypeDef, error) {
	var kind core.TypeDefKind
	if spec.goType.Kind() == types.Invalid {
		// NOTE: this is odd, but it doesn't matter, because the module won't
		// pass the compilation step if there are invalid types - we just want
		// to not error out horribly in codegen
		kind = core.TypeDefKindVoid
	} else {
		switch spec.goType.Info() {
		case types.IsString:
			kind = core.TypeDefKindString
		case types.IsInteger:
			kind = core.TypeDefKindInteger
		case types.IsBoolean:
			kind = core.TypeDefKindBoolean
		case types.IsFloat:
			kind = core.TypeDefKindFloat
		default:
			return nil, fmt.Errorf("unsupported basic type: %+v", spec.goType)
		}
	}
	var def *core.TypeDef
	if spec.scalarType != nil {
		def = (&core.TypeDef{}).WithScalar(spec.scalarType.Obj().Name(), "")
	} else {
		def = (&core.TypeDef{}).WithKind(kind)
	}
	if spec.isPtr {
		def = def.WithOptional(true)
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

func (spec *parsedSliceType) TypeDefObject() (*core.TypeDef, error) {
	underlyingTypeDef, err := spec.underlying.TypeDefObject()
	if err != nil {
		return nil, fmt.Errorf("failed to generate underlying typedef: %w", err)
	}
	return (&core.TypeDef{}).WithListOf(underlyingTypeDef), nil
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

func (spec *parsedObjectTypeReference) TypeDefObject() (*core.TypeDef, error) {
	return (&core.TypeDef{}).WithObject(spec.name, "", nil), nil
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

func (spec *parsedIfaceTypeReference) TypeDefObject() (*core.TypeDef, error) {
	return (&core.TypeDef{}).WithInterface(spec.name, "", nil), nil
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

type sourceMap struct {
	filename string
	line     int
	column   int
}

func (ps *parseState) sourceMap(item interface{ Pos() token.Pos }) *sourceMap {
	pos := item.Pos()
	position := ps.fset.Position(pos)

	filename, err := filepath.Rel(ps.pkg.Module.Dir, position.Filename)
	if err != nil {
		filename = position.Filename
	}

	return &sourceMap{
		filename: filename,
		line:     position.Line,
		column:   position.Column,
	}
}

func (spec *sourceMap) TypeDefCode() *Statement {
	return Qual("dag", "SourceMap").Call(Lit(spec.filename), Lit(spec.line), Lit(spec.column))
}

func (spec *sourceMap) TypeDefObject() *core.SourceMap {
	return &core.SourceMap{
		Filename: spec.filename,
		Line:     spec.line,
		Column:   spec.column,
	}
}

func coreSourceMap(srcMap *sourceMap) *core.SourceMap {
	if srcMap == nil {
		return nil
	}
	return srcMap.TypeDefObject()
}
