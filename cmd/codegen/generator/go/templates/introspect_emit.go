package templates

import (
	"encoding/json"
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/iancoleman/strcase"
)

// introspectTypeRef converts a ParsedType into its introspection TypeRef tree.
//
// Optionality mirrors the existing TypeDef(dag) methods and WithOptional semantics:
//   - non-pointer scalar/object/enum → wrapped in NON_NULL
//   - pointer scalar/object/enum → left nullable (no NON_NULL wrapper)
//   - non-pointer slice → NON_NULL{ LIST{ <elem TypeRef> } }
//   - interface refs are always non-nullable (interfaces are never pointers in Go)
//
// For void / unknown types a NON_NULL{SCALAR Void} fallback is returned,
// matching the behavior of TypeDef which returns Void for nil returnSpec.
func introspectTypeRef(spec ParsedType) *introspection.TypeRef {
	switch s := spec.(type) {
	case *parsedPrimitiveType:
		return introspectPrimitiveTypeRef(s)

	case *parsedSliceType:
		elemRef := introspectTypeRef(s.underlying)
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind:   introspection.TypeKindList,
				OfType: elemRef,
			},
		}

	case *parsedObjectTypeReference:
		inner := &introspection.TypeRef{
			Kind: introspection.TypeKindObject,
			Name: s.name,
		}
		if s.isPtr {
			return inner
		}
		return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: inner}

	case *parsedIfaceTypeReference:
		// Interface references are always non-nullable in the dagger type system.
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindInterface,
				Name: s.name,
			},
		}

	case *parsedEnumTypeReference:
		inner := &introspection.TypeRef{
			Kind: introspection.TypeKindEnum,
			Name: s.name,
		}
		if s.isPtr {
			return inner
		}
		return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: inner}

	default:
		// Fallback: matches TypeDef void behaviour for unrecognised types.
		return introspectVoidRef()
	}
}

// introspectPrimitiveTypeRef handles *parsedPrimitiveType → TypeRef conversion.
func introspectPrimitiveTypeRef(spec *parsedPrimitiveType) *introspection.TypeRef {
	var scalarName string
	if spec.scalarType != nil {
		// Custom scalar type declared in the module (e.g. `type CacheVolumeID string`).
		scalarName = spec.scalarType.Obj().Name()
	} else if spec.goType.Kind() == types.Invalid {
		// Invalid type — emit Void, matching TypeDef's Void handling.
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindScalar,
				Name: string(introspection.ScalarVoid),
			},
		}
	} else {
		switch spec.goType.Info() {
		case types.IsString:
			scalarName = string(introspection.ScalarString)
		case types.IsInteger:
			scalarName = string(introspection.ScalarInt)
		case types.IsBoolean:
			scalarName = string(introspection.ScalarBoolean)
		case types.IsFloat:
			scalarName = string(introspection.ScalarFloat)
		default:
			// An unsupported basic kind cannot appear in valid module code (the
			// parser would have rejected it before we reach this point), so we
			// emit Void as a safe fallback.  Note: TypeDef only emits Void for
			// types.Invalid; a genuine unsupported basic kind would be an error
			// there.  We don't propagate an error here because such code can't
			// compile anyway.
			return introspectVoidRef()
		}
	}

	inner := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: scalarName}
	if spec.isPtr {
		return inner
	}
	return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: inner}
}

// introspectVoidRef returns the canonical TypeRef for a void / unrecognised return.
// It mirrors the TypeDef behaviour: Void scalar, wrapped in NON_NULL (then optional).
// The Field's TypeRef is set directly to this; callers that need the optional
// wrapping (as TypeDef uses WithOptional) can wrap further if needed.
func introspectVoidRef() *introspection.TypeRef {
	return &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: string(introspection.ScalarVoid),
		},
	}
}

// ---------------------------------------------------------------------------
// Type emitters: object, interface, enum
// ---------------------------------------------------------------------------

// introspectObject converts a parsedObjectType to an introspection.Type.
// It mirrors parsedObjectType.TypeDef(dag).
func introspectObject(spec *parsedObjectType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindObject,
		Name:        spec.name,
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}

	// Methods → Fields
	for _, m := range spec.methods {
		t.Fields = append(t.Fields, introspectMethod(m))
	}

	// Struct fields → Fields (public, non-private)
	for _, f := range spec.fields {
		if f.isPrivate {
			continue
		}
		field := &introspection.Field{
			Name:        f.name,
			Description: strings.TrimSpace(f.doc),
			TypeRef:     introspectTypeRef(f.typeSpec),
			Args:        introspection.InputValues{},
		}
		if f.deprecated != nil {
			field.IsDeprecated = true
			reason := strings.TrimSpace(*f.deprecated)
			field.DeprecationReason = &reason
		}
		t.Fields = append(t.Fields, field)
	}

	return t
}

// introspectMethod converts a funcTypeSpec to an introspection Field.
// Used for both object methods and interface methods.
func introspectMethod(m *funcTypeSpec) *introspection.Field {
	var typeRef *introspection.TypeRef
	if m.returnSpec == nil {
		typeRef = introspectVoidRef()
	} else {
		typeRef = introspectTypeRef(m.returnSpec)
	}

	field := &introspection.Field{
		Name:        strcase.ToLowerCamel(m.name),
		Description: strings.TrimSpace(m.doc),
		TypeRef:     typeRef,
		Args:        introspectFuncArgs(m),
	}

	if m.deprecated != nil {
		field.IsDeprecated = true
		reason := strings.TrimSpace(*m.deprecated)
		field.DeprecationReason = &reason
	}

	return field
}

// introspectFuncArgs converts a funcTypeSpec's paramSpecs to InputValues,
// skipping context args and honouring the optional flag (matching TypeDef's
// WithOptional wrapping which makes a type nullable).
func introspectFuncArgs(m *funcTypeSpec) introspection.InputValues {
	var args introspection.InputValues
	for _, arg := range m.argSpecs {
		if arg.isContext {
			continue
		}
		iv := introspectArg(arg)
		args = append(args, iv)
	}
	return args
}

// introspectArg converts a single paramSpec to an InputValue.
func introspectArg(arg paramSpec) introspection.InputValue {
	typeRef := introspectTypeRef(arg.typeSpec)
	if arg.isOptional() {
		// Optional args become nullable: strip the NON_NULL wrapper if present.
		if typeRef.Kind == introspection.TypeKindNonNull {
			typeRef = typeRef.OfType
		}
	}

	iv := introspection.InputValue{
		Name:        arg.name,
		Description: strings.TrimSpace(arg.description),
		TypeRef:     typeRef,
	}

	if arg.hasDefaultValue {
		var defaultValue string
		if enumType, ok := arg.typeSpec.(*parsedEnumTypeReference); ok {
			// Mirror module_funcs.go TypeDefFunc: resolve the Go const value to
			// the enum member name via the enum's lookup table, then JSON-quote
			// it.  The raw defaultValue is the string the user wrote in the
			// +default pragma (either the member name or the underlying value).
			v, ok := arg.defaultValue.(string)
			if ok {
				if res := enumType.lookup(v); res != nil {
					defaultValue = strconv.Quote(res.name)
					iv.DefaultValue = &defaultValue
				}
			}
		} else {
			encoded, err := json.Marshal(arg.defaultValue)
			if err == nil {
				defaultValue = string(encoded)
				iv.DefaultValue = &defaultValue
			}
		}
	}

	if arg.deprecated != nil {
		iv.IsDeprecated = true
		reason := strings.TrimSpace(*arg.deprecated)
		iv.DeprecationReason = &reason
	}

	return iv
}

// introspectInterface converts a parsedIfaceType to an introspection.Type.
// Mirrors parsedIfaceType.TypeDef(dag).
func introspectInterface(spec *parsedIfaceType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindInterface,
		Name:        spec.name,
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}
	for _, m := range spec.methods {
		t.Fields = append(t.Fields, introspectMethod(m))
	}
	return t
}

// introspectEnum converts a parsedEnumType to an introspection.Type.
// Mirrors parsedEnumType.TypeDef(dag).
func introspectEnum(spec *parsedEnumType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindEnum,
		Name:        spec.name,
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}
	for _, v := range spec.values {
		ev := introspection.EnumValue{
			Name:        v.name,
			Description: strings.TrimSpace(v.doc),
		}
		if v.deprecated != nil {
			ev.IsDeprecated = true
			reason := strings.TrimSpace(*v.deprecated)
			ev.DeprecationReason = &reason
		}
		t.EnumValues = append(t.EnumValues, ev)
	}
	return t
}

// ---------------------------------------------------------------------------
// Top-level emitter: ModuleIntrospectionJSON
// ---------------------------------------------------------------------------

// ModuleIntrospectionJSON walks the module's parsed types (via the same
// visitor the dispatcher uses) and emits a minimal introspection Response
// containing the module's object/interface/enum types plus a Query type
// carrying the module's constructor field. The output is the `moduleTypes`
// argument to schemaTools.Merge.
//
// The Query type is populated as follows:
//  1. If any object type found during walking matches (case-insensitively) the
//     camelCase of moduleName, that object is the "main object" and its
//     constructor args (if any) are carried on the Query field.
//  2. If no such object is found the Query type is still emitted (merge will
//     synthesise a no-arg constructor if the main object lands in the target
//     schema via other means).
func (funcs goTemplateFuncs) ModuleIntrospectionJSON(moduleName string) ([]byte, error) {
	var moduleTypes introspection.Types
	var mainObject *parsedObjectType

	mainObjCamel := strcase.ToCamel(moduleName)

	err := funcs.visitTypes(false, &visitorFuncs{
		RootVisitor: func(_ string) error { return nil },
		StructVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedObjectType, _ *types.Struct) error {
			moduleTypes = append(moduleTypes, introspectObject(spec))
			if strcase.ToCamel(spec.name) == mainObjCamel {
				mainObject = spec
			}
			return nil
		},
		IfaceVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedIfaceType, _ *types.Interface) error {
			moduleTypes = append(moduleTypes, introspectInterface(spec))
			return nil
		},
		EnumVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedEnumType, _ *types.Basic) error {
			moduleTypes = append(moduleTypes, introspectEnum(spec))
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("visit module types: %w", err)
	}

	// Build the Query type with the module's constructor field.
	queryType := &introspection.Type{
		Kind:       introspection.TypeKindObject,
		Name:       "Query",
		Interfaces: []*introspection.Type{},
	}
	if mainObject != nil {
		ctorRef := &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: mainObject.name,
			},
		}
		field := &introspection.Field{
			Name:    strcase.ToLowerCamel(moduleName),
			TypeRef: ctorRef,
			Args:    introspection.InputValues{},
		}
		if mainObject.constructor != nil {
			field.Args = introspectFuncArgs(mainObject.constructor)
		}
		queryType.Fields = append(queryType.Fields, field)
	}
	moduleTypes = append(moduleTypes, queryType)

	resp := introspection.Response{
		Schema: &introspection.Schema{
			QueryType: struct {
				Name string `json:"name,omitempty"`
			}{Name: "Query"},
			Types: moduleTypes,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal introspection response: %w", err)
	}
	return data, nil
}
