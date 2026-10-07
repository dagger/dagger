package templates

import (
	"encoding/json"
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// introspectTypeRef converts a ParsedType into its introspection TypeRef tree.
//
// Nullability mirrors the module TypeDefCode() registration exactly, so the schema this
// emits matches what the engine builds from the same source:
//   - non-pointer scalar/object/enum/slice → NON_NULL
//   - pointer scalar → nullable: only parsedPrimitiveType.TypeDefCode marks a
//     pointer optional (WithOptional), so *string is an optional scalar
//   - pointer object/enum → still NON_NULL: their TypeDefCode never calls
//     WithOptional, so a pointer only changes the Go type, not schema nullability
//     (e.g. a required *Directory arg, enforced by assertNotNil in the client)
//   - interface refs → always NON_NULL (interfaces are never pointers in Go)
//
// Argument-level +optional is applied separately by introspectArg, which strips
// the NON_NULL wrapper; this function only encodes the type's own nullability.
//
// For void / unknown types a nullable SCALAR Void fallback is returned (see
// introspectVoidRef), matching the engine which marks Void always optional.
func introspectTypeRef(n moduleNamer, spec ParsedType) *introspection.TypeRef {
	switch s := spec.(type) {
	case *parsedPrimitiveType:
		return introspectPrimitiveTypeRef(s)

	case *parsedSliceType:
		elemRef := introspectTypeRef(n, s.underlying)
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind:   introspection.TypeKindList,
				OfType: elemRef,
			},
		}

	case *parsedObjectTypeReference:
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: n.typeName(s.name, s.moduleName),
			},
		}

	case *parsedIfaceTypeReference:
		// Interface references are always non-nullable in the dagger type system.
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindInterface,
				Name: n.typeName(s.name, s.moduleName),
			},
		}

	case *parsedEnumTypeReference:
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindEnum,
				Name: n.typeName(s.name, s.moduleName),
			},
		}

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
// Void is emitted nullable (no NON_NULL wrapper) because the engine's TypeDef
// path always marks a Void return optional (voidDef / TypeDefCode use
// WithOptional(true); core documents VOID_KIND's outer TypeDef as always
// Optional). A NON_NULL Void here would diverge from the engine-built schema.
func introspectVoidRef() *introspection.TypeRef {
	return &introspection.TypeRef{
		Kind: introspection.TypeKindScalar,
		Name: string(introspection.ScalarVoid),
	}
}

// introspectObject converts a parsedObjectType to an introspection.Type.
// It mirrors parsedObjectType.TypeDefCode(), with the names the engine gives
// the module's types and fields (see moduleNamer).
func introspectObject(n moduleNamer, spec *parsedObjectType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindObject,
		Name:        n.typeName(spec.name, spec.moduleName),
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}

	// Methods → Fields
	for _, m := range spec.methods {
		if n.fieldName(m.name) == "id" {
			// "id" is engine-reserved (Node): registration rejects it with an
			// actionable error. Skip it here so the generated bindings still
			// compile and that error is the one the user sees.
			continue
		}
		t.Fields = append(t.Fields, introspectMethod(n, m))
	}

	// Struct fields → Fields (public, non-private)
	for _, f := range spec.fields {
		if f.isPrivate {
			continue
		}
		if n.fieldName(f.name) == "id" {
			// engine-reserved, see above
			continue
		}
		field := &introspection.Field{
			Name:        n.fieldName(f.name),
			Description: strings.TrimSpace(f.doc),
			TypeRef:     introspectTypeRef(n, f.typeSpec),
			Args:        introspection.InputValues{},
		}
		if f.deprecated != nil {
			field.IsDeprecated = true
			reason := strings.TrimSpace(*f.deprecated)
			field.DeprecationReason = &reason
		}
		t.Fields = append(t.Fields, field)
	}

	t.Fields = append(t.Fields, introspectNodeIDField(t.Name))

	return t
}

// introspectNodeIDField mirrors the `id` field the engine adds to every module
// object and interface (Node). The bindings key their ID marshalling on it, so
// without it a module type could not be passed as an object argument.
func introspectNodeIDField(typeName string) *introspection.Field {
	return &introspection.Field{
		Name:        "id",
		Description: fmt.Sprintf("A unique identifier for this %s.", typeName),
		TypeRef:     introspectIDRef(),
		Args:        introspection.InputValues{},
	}
}

// introspectMethod converts a funcTypeSpec to an introspection Field.
// Used for both object methods and interface methods.
func introspectMethod(n moduleNamer, m *funcTypeSpec) *introspection.Field {
	var typeRef *introspection.TypeRef
	if m.returnSpec == nil {
		typeRef = introspectVoidRef()
	} else {
		typeRef = introspectTypeRef(n, m.returnSpec)
	}

	field := &introspection.Field{
		Name:        n.fieldName(m.name),
		Description: strings.TrimSpace(m.doc),
		TypeRef:     typeRef,
		Args:        introspectFuncArgs(n, m),
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
func introspectFuncArgs(n moduleNamer, m *funcTypeSpec) introspection.InputValues {
	var args introspection.InputValues
	for _, arg := range m.argSpecs {
		if arg.isContext {
			continue
		}
		iv := introspectArg(n, arg)
		args = append(args, iv)
	}
	return args
}

// introspectArgTypeRef converts an argument's ParsedType to its schema
// TypeRef. Object and interface arguments are passed by ID: the engine's
// TypeDef.ToInput maps them to AnyID (an `ID` scalar) and stamps an
// @expectedType directive naming the target type, which the binding generator
// uses to recover the concrete parameter type. expectedType is the name for
// that directive; empty when the argument is not object/interface-typed.
func introspectArgTypeRef(n moduleNamer, spec ParsedType) (ref *introspection.TypeRef, expectedType string) {
	switch s := spec.(type) {
	case *parsedSliceType:
		elemRef, expected := introspectArgTypeRef(n, s.underlying)
		return &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind:   introspection.TypeKindList,
				OfType: elemRef,
			},
		}, expected

	case *parsedObjectTypeReference:
		return introspectIDRef(), n.typeName(s.name, s.moduleName)

	case *parsedIfaceTypeReference:
		return introspectIDRef(), n.typeName(s.name, s.moduleName)

	default:
		return introspectTypeRef(n, spec), ""
	}
}

func introspectIDRef() *introspection.TypeRef {
	return &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "ID",
		},
	}
}

// introspectArg converts a single paramSpec to an InputValue.
func introspectArg(n moduleNamer, arg paramSpec) introspection.InputValue {
	typeRef, expectedType := introspectArgTypeRef(n, arg.typeSpec)
	// Strip the NON_NULL wrapper exactly where the engine calls WithOptional:
	// arg.optional (set by +optional, defaultPath and defaultAddress — see
	// TypeDefCode's `if argSpec.optional`). We deliberately do NOT use
	// paramSpec.isOptional() here — it also treats defaults, variadic args and
	// every pointer as optional, but the engine registers those as required
	// (a +default arg stays NON_NULL with a defaultValue; pointer-ness alone
	// only makes scalars optional, already encoded by introspectTypeRef).
	if arg.optional {
		if typeRef.Kind == introspection.TypeKindNonNull {
			typeRef = typeRef.OfType
		}
	}

	iv := introspection.InputValue{
		Name:        n.fieldName(arg.name),
		Description: strings.TrimSpace(arg.description),
		TypeRef:     typeRef,
	}
	if expectedType != "" {
		name := strconv.Quote(expectedType)
		iv.Directives = append(iv.Directives, &introspection.Directive{
			Name: "expectedType",
			Args: []*introspection.DirectiveArg{{Name: "name", Value: &name}},
		})
	}

	if arg.hasDefaultValue {
		var defaultValue string
		if enumType, ok := arg.typeSpec.(*parsedEnumTypeReference); ok {
			// Mirror funcTypeSpec.TypeDefCode: resolve the Go const value to
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
// Mirrors parsedIfaceType.TypeDefCode().
func introspectInterface(n moduleNamer, spec *parsedIfaceType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindInterface,
		Name:        n.typeName(spec.name, spec.moduleName),
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}
	for _, m := range spec.methods {
		if n.fieldName(m.name) == "id" {
			// engine-reserved, see introspectObject
			continue
		}
		t.Fields = append(t.Fields, introspectMethod(n, m))
	}
	t.Fields = append(t.Fields, introspectNodeIDField(t.Name))
	return t
}

// introspectEnum converts a parsedEnumType to an introspection.Type.
// Mirrors parsedEnumType.TypeDefCode().
func introspectEnum(n moduleNamer, spec *parsedEnumType) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindEnum,
		Name:        n.typeName(spec.name, spec.moduleName),
		Description: strings.TrimSpace(spec.doc),
		Interfaces:  []*introspection.Type{},
	}
	for _, v := range spec.values {
		ev := introspection.EnumValue{
			// Emitting a different name than the engine registers would make
			// self-call bindings send unknown enum values.
			Name:        n.enumMemberName(v.name),
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

// isConventionalGraphQLEnumMemberName mirrors the engine's check
// (core/gqlformat.go) for member names already written in GraphQL's member
// convention, such as HTTP2, which are kept as they are.
func isConventionalGraphQLEnumMemberName(name string) bool {
	if name == "" || strings.HasPrefix(name, "__") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && ((c >= '0' && c <= '9') || c == '_') {
			continue
		}
		return false
	}
	return true
}

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

	n := funcs.moduleNamer()
	mainObjName := n.objectName(moduleName)

	err := funcs.visitTypes(false, &visitorFuncs{
		RootVisitor: func(_ string) error { return nil },
		StructVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedObjectType, _ *types.Struct) error {
			if spec.isCollection {
				collection, batch, err := introspectCollection(n, spec)
				if err != nil {
					return err
				}
				moduleTypes = append(moduleTypes, collection)
				if batch != nil {
					moduleTypes = append(moduleTypes, batch)
				}
			} else {
				moduleTypes = append(moduleTypes, introspectObject(n, spec))
			}
			if n.objectName(spec.name) == mainObjName {
				mainObject = spec
			}
			return nil
		},
		IfaceVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedIfaceType, _ *types.Interface) error {
			moduleTypes = append(moduleTypes, introspectInterface(n, spec))
			return nil
		},
		EnumVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, spec *parsedEnumType, _ *types.Basic) error {
			moduleTypes = append(moduleTypes, introspectEnum(n, spec))
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("visit module types: %w", err)
	}

	// A module type whose namespaced name is already taken by a core or
	// dependency type cannot be installed — the engine rejects the module at
	// load with an actionable error. Skip it here so codegen doesn't mask
	// that error with a merge conflict; references to it resolve to the
	// existing type, which is how the engine resolves them too.
	kept := moduleTypes[:0]
	for _, t := range moduleTypes {
		if funcs.schema != nil && funcs.schema.Types.Get(t.Name) != nil {
			if mainObject != nil && n.objectName(mainObject.name) == t.Name {
				mainObject = nil
			}
			continue
		}
		kept = append(kept, t)
	}
	moduleTypes = kept

	// Legacy schema views (pre v0.21 cutover) carry a per-type <T>ID scalar
	// for every object and interface; the Go templates render id fields as
	// that alias (`ID(ctx) (TestID, error)` with `type TestID = ID`), so
	// without the scalar the generated bindings don't compile.
	if funcs.legacyGoSDKCompat() {
		var idScalars introspection.Types
		for _, t := range moduleTypes {
			if t.Kind != introspection.TypeKindObject && t.Kind != introspection.TypeKindInterface {
				continue
			}
			idScalars = append(idScalars, &introspection.Type{
				Kind:        introspection.TypeKindScalar,
				Name:        t.Name + "ID",
				Description: "A unique identifier for an object.",
			})
		}
		moduleTypes = append(moduleTypes, idScalars...)
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
				Name: n.typeName(mainObject.name, mainObject.moduleName),
			},
		}
		field := &introspection.Field{
			Name:    n.fieldName(moduleName),
			TypeRef: ctorRef,
			Args:    introspection.InputValues{},
		}
		if mainObject.constructor != nil {
			field.Args = introspectFuncArgs(n, mainObject.constructor)
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
