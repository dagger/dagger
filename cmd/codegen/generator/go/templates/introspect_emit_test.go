package templates

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// buildTestFuncs builds a minimal goTemplateFuncs from in-memory Go source.
func buildTestFuncs(t *testing.T, moduleName string, sources map[string]string) goTemplateFuncs {
	t.Helper()
	fset := token.NewFileSet()
	var syntax []*ast.File
	for filename, src := range sources {
		f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
		require.NoErrorf(t, err, "parse %q", filename)
		syntax = append(syntax, f)
	}

	// Type-check without any additional imports (simple types only).
	typesPkg, err := (&types.Config{
		Importer: nil,
	}).Check("example.com/testmodule", fset, syntax, nil)
	require.NoError(t, err)

	pkg := &packages.Package{
		Types:  typesPkg,
		Syntax: syntax,
		Fset:   fset,
		Module: &packages.Module{Dir: "."},
	}

	return goTemplateFuncs{
		cfg: generator.Config{
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName: moduleName,
			},
		},
		modulePkg:  pkg,
		moduleFset: fset,
	}
}

func TestIntrospectTypeRef_Primitive(t *testing.T) {
	// non-pointer string -> NON_NULL{SCALAR String}
	ref := introspectTypeRef(&parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindScalar, ref.OfType.Kind)
	require.Equal(t, "String", ref.OfType.Name)
}

func TestIntrospectTypeRef_PrimitivePointer(t *testing.T) {
	// pointer string -> nullable SCALAR String (no NON_NULL)
	ref := introspectTypeRef(&parsedPrimitiveType{goType: types.Typ[types.String], isPtr: true})
	require.Equal(t, introspection.TypeKindScalar, ref.Kind)
	require.Equal(t, "String", ref.Name)
	require.Nil(t, ref.OfType)
}

func TestIntrospectTypeRef_PrimitiveInt(t *testing.T) {
	ref := introspectTypeRef(&parsedPrimitiveType{goType: types.Typ[types.Int], isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, "Int", ref.OfType.Name)
}

func TestIntrospectTypeRef_PrimitiveBool(t *testing.T) {
	ref := introspectTypeRef(&parsedPrimitiveType{goType: types.Typ[types.Bool], isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, "Boolean", ref.OfType.Name)
}

func TestIntrospectTypeRef_PrimitiveFloat(t *testing.T) {
	ref := introspectTypeRef(&parsedPrimitiveType{goType: types.Typ[types.Float64], isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, "Float", ref.OfType.Name)
}

func TestIntrospectTypeRef_Slice(t *testing.T) {
	// []string -> NON_NULL{LIST{NON_NULL{SCALAR String}}}
	elem := &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false}
	ref := introspectTypeRef(&parsedSliceType{
		goType:     types.NewSlice(types.Typ[types.String]),
		underlying: elem,
	})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindList, ref.OfType.Kind)
	require.Equal(t, introspection.TypeKindNonNull, ref.OfType.OfType.Kind)
	require.Equal(t, "String", ref.OfType.OfType.OfType.Name)
}

func TestIntrospectTypeRef_ObjectRef(t *testing.T) {
	// non-pointer object ref -> NON_NULL{OBJECT Foo}
	ref := introspectTypeRef(&parsedObjectTypeReference{name: "Foo", isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindObject, ref.OfType.Kind)
	require.Equal(t, "Foo", ref.OfType.Name)
}

func TestIntrospectTypeRef_ObjectRefPointer(t *testing.T) {
	// pointer object ref -> NON_NULL{OBJECT Foo}: a pointer only changes the Go
	// type, not schema nullability (object TypeDefCode never sets WithOptional).
	ref := introspectTypeRef(&parsedObjectTypeReference{name: "Foo", isPtr: true})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindObject, ref.OfType.Kind)
	require.Equal(t, "Foo", ref.OfType.Name)
}

func TestIntrospectTypeRef_IfaceRef(t *testing.T) {
	// interface ref -> NON_NULL{INTERFACE MyIface}
	ref := introspectTypeRef(&parsedIfaceTypeReference{name: "MyIface"})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindInterface, ref.OfType.Kind)
	require.Equal(t, "MyIface", ref.OfType.Name)
}

func TestIntrospectTypeRef_EnumRef(t *testing.T) {
	// enum ref -> NON_NULL{ENUM Status}
	ref := introspectTypeRef(&parsedEnumTypeReference{name: "Status", isPtr: false})
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindEnum, ref.OfType.Kind)
	require.Equal(t, "Status", ref.OfType.Name)
}

func TestIntrospectObject_Basic(t *testing.T) {
	obj := &parsedObjectType{
		name: "Test",
		doc:  "A test object.",
		methods: []*funcTypeSpec{
			{
				name: "Echo",
				argSpecs: []paramSpec{
					{
						name:     "s",
						typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
					},
				},
				returnSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
			},
		},
	}

	it := introspectObject(obj)
	require.Equal(t, introspection.TypeKindObject, it.Kind)
	require.Equal(t, "Test", it.Name)
	require.Equal(t, "A test object.", it.Description)
	require.Len(t, it.Fields, 2) // declared field + engine-added id

	f := it.Fields[0]
	require.Equal(t, "echo", f.Name)
	// return type: NON_NULL{SCALAR String}
	require.Equal(t, introspection.TypeKindNonNull, f.TypeRef.Kind)
	require.Equal(t, "String", f.TypeRef.OfType.Name)
	// arg
	require.Len(t, f.Args, 1)
	require.Equal(t, "s", f.Args[0].Name)
	require.Equal(t, introspection.TypeKindNonNull, f.Args[0].TypeRef.Kind)
}

func TestIntrospectObject_VoidReturn(t *testing.T) {
	obj := &parsedObjectType{
		name: "Doer",
		methods: []*funcTypeSpec{
			{
				name:       "DoNothing",
				argSpecs:   []paramSpec{},
				returnSpec: nil, // void
			},
		},
	}

	it := introspectObject(obj)
	require.Len(t, it.Fields, 2) // declared field + engine-added id
	f := it.Fields[0]
	require.Equal(t, "doNothing", f.Name)
	// Void return: nullable SCALAR Void (the engine always marks Void optional).
	require.Equal(t, introspection.TypeKindScalar, f.TypeRef.Kind)
	require.Equal(t, "Void", f.TypeRef.Name)
	require.Nil(t, f.TypeRef.OfType)
}

func TestIntrospectObject_WithField(t *testing.T) {
	obj := &parsedObjectType{
		name: "Config",
		fields: []*fieldSpec{
			{
				name:     "value",
				typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
				doc:      "The value.",
			},
		},
	}

	it := introspectObject(obj)
	require.Len(t, it.Fields, 2) // declared field + engine-added id
	f := it.Fields[0]
	require.Equal(t, "value", f.Name)
	require.Equal(t, "The value.", f.Description)
}

func TestIntrospectObject_SkipContextArg(t *testing.T) {
	obj := &parsedObjectType{
		name: "Runner",
		methods: []*funcTypeSpec{
			{
				name: "Run",
				argSpecs: []paramSpec{
					{name: "ctx", isContext: true},
					{name: "cmd", typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false}},
				},
				returnSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
			},
		},
	}

	it := introspectObject(obj)
	f := it.Fields[0]
	// ctx must be skipped
	require.Len(t, f.Args, 1)
	require.Equal(t, "cmd", f.Args[0].Name)
}

func TestIntrospectInterface_Basic(t *testing.T) {
	iface := &parsedIfaceType{
		name: "Greeter",
		methods: []*funcTypeSpec{
			{
				name:       "Hello",
				argSpecs:   []paramSpec{},
				returnSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
			},
		},
	}

	it := introspectInterface(iface)
	require.Equal(t, introspection.TypeKindInterface, it.Kind)
	require.Equal(t, "Greeter", it.Name)
	require.Len(t, it.Fields, 2) // declared field + engine-added id
	require.Equal(t, "hello", it.Fields[0].Name)
}

func TestIntrospectEnum_Basic(t *testing.T) {
	enum := &parsedEnumType{
		name: "Status",
		values: []*parsedEnumMember{
			{name: "Active", doc: "An active status.", value: "ACTIVE"},
			{name: "Inactive", value: "INACTIVE"},
		},
	}

	it := introspectEnum(enum)
	require.Equal(t, introspection.TypeKindEnum, it.Kind)
	require.Equal(t, "Status", it.Name)
	require.Len(t, it.EnumValues, 2)
	require.Equal(t, "ACTIVE", it.EnumValues[0].Name)
	require.Equal(t, "An active status.", it.EnumValues[0].Description)
	require.Equal(t, "INACTIVE", it.EnumValues[1].Name)
}

// TestIntrospectObject_SkipsReservedID verifies user-declared "id" members are
// not emitted: the name is engine-reserved (Node), so emitting them alongside
// the synthetic id would double-declare it in the bindings and mask the
// engine's actionable reserved-name error with a compile failure.
func TestIntrospectObject_SkipsReservedID(t *testing.T) {
	obj := &parsedObjectType{
		name: "Test",
		fields: []*fieldSpec{
			{name: "id", typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String]}},
		},
		methods: []*funcTypeSpec{
			{name: "Id", returnSpec: &parsedPrimitiveType{goType: types.Typ[types.String]}},
			{name: "Echo", returnSpec: &parsedPrimitiveType{goType: types.Typ[types.String]}},
		},
	}

	it := introspectObject(obj)
	require.Len(t, it.Fields, 2, "user id field and Id method skipped; echo + synthetic id remain")
	require.Equal(t, "echo", it.Fields[0].Name)
	require.Equal(t, "id", it.Fields[1].Name)
	require.Equal(t, "ID", it.Fields[1].TypeRef.OfType.Name,
		"the remaining id is the synthetic engine one")
}

// TestNamespaceTypeName verifies the mirror of the engine's namespaceObject:
// module types are installed prefixed with the module name (main object
// exempt), so the emitted schema must use the same names or a module type
// shadowing a core/dependency type name would collide at merge time.
func TestNamespaceTypeName(t *testing.T) {
	for _, tc := range []struct {
		typeName, moduleName, want string
	}{
		{"Test", "test", "Test"},               // main object keeps the module name
		{"Color", "test", "TestColor"},         // plain type gets prefixed
		{"Container", "test", "TestContainer"}, // shadowing a core name is namespaced away
		{"TestWorker", "test", "TestWorker"},   // already prefixed on a word boundary
		{"Postman", "post", "PostPostman"},     // prefix without word boundary still namespaces
		{"my-type", "my-mod", "MyModMyType"},   // kebab inputs camelize like the engine
	} {
		require.Equal(t, tc.want, namespaceTypeName(tc.typeName, tc.moduleName),
			"namespaceTypeName(%q, %q)", tc.typeName, tc.moduleName)
	}
}

// TestIntrospectTypeName_LocalVsForeign verifies only module-local types are
// namespaced: refs to core/dependency types (moduleName == "") already carry
// their final schema name.
func TestIntrospectTypeName_LocalVsForeign(t *testing.T) {
	localRef := introspectTypeRef(&parsedObjectTypeReference{name: "Container", moduleName: "test"})
	require.Equal(t, "TestContainer", localRef.OfType.Name,
		"module-local ref must be namespaced")

	coreRef := introspectTypeRef(&parsedObjectTypeReference{name: "Container"})
	require.Equal(t, "Container", coreRef.OfType.Name,
		"core ref must be untouched")

	localEnum := introspectEnum(&parsedEnumType{name: "Color", moduleName: "test"})
	require.Equal(t, "TestColor", localEnum.Name)

	localObj := introspectObject(&parsedObjectType{name: "Worker", moduleName: "test"})
	require.Equal(t, "TestWorker", localObj.Name)
}

// TestIntrospectEnum_ConventionalMemberNames verifies member naming matches the
// engine's gqlEnumMemberName: already-conventional names (HTTP2, TLS13) are
// kept as-is, everything else becomes SCREAMING_SNAKE. Divergence here makes
// self-call bindings send unknown enum values.
func TestIntrospectEnum_ConventionalMemberNames(t *testing.T) {
	enum := &parsedEnumType{
		name: "Proto",
		values: []*parsedEnumMember{
			{name: "HTTP2", value: "http/2"},
			{name: "TLS13", value: "tls/1.3"},
			{name: "PlainText", value: "plain"},
		},
	}

	it := introspectEnum(enum)
	require.Len(t, it.EnumValues, 3)
	require.Equal(t, "HTTP2", it.EnumValues[0].Name,
		"conventional names must be kept as-is, like the engine does")
	require.Equal(t, "TLS13", it.EnumValues[1].Name)
	require.Equal(t, "PLAIN_TEXT", it.EnumValues[2].Name)
}

// buildTestFuncsForRoundTrip creates a goTemplateFuncs from Go source that
// the visitTypes call can walk (no DaggerObject interface needed since strict=false).
func buildTestFuncsForRoundTrip(t *testing.T, moduleName string) goTemplateFuncs {
	t.Helper()
	src := `package main

// Test is the main test object.
type Test struct{}

// Echo returns s unchanged.
func (t *Test) Echo(s string) string { return s }
`
	return buildTestFuncs(t, moduleName, map[string]string{"main.go": src})
}

func TestModuleIntrospectionJSON_RoundTripsThroughMerge(t *testing.T) {
	funcs := buildTestFuncsForRoundTrip(t, "test")

	jsonBytes, err := funcs.ModuleIntrospectionJSON("test")
	require.NoError(t, err)
	require.NotEmpty(t, jsonBytes)

	var resp introspection.Response
	require.NoError(t, json.Unmarshal(jsonBytes, &resp))
	require.NotNil(t, resp.Schema, "expected __schema field")

	// The Test object must be present.
	testType := resp.Schema.Types.Get("Test")
	require.NotNil(t, testType, "Types must contain 'Test'")
	require.Equal(t, introspection.TypeKindObject, testType.Kind)

	// The Query type must carry a 'test' constructor field.
	resp.Schema.QueryType.Name = "Query" // set so Schema.Query() works
	q := resp.Schema.Types.Get("Query")
	require.NotNil(t, q, "Types must contain 'Query'")

	var ctorField *introspection.Field
	for _, f := range q.Fields {
		if f.Name == "test" {
			ctorField = f
			break
		}
	}
	require.NotNil(t, ctorField, "Query must have a 'test' field")
	require.Equal(t, introspection.TypeKindNonNull, ctorField.TypeRef.Kind)
	require.Equal(t, introspection.TypeKindObject, ctorField.TypeRef.OfType.Kind)
	require.Equal(t, "Test", ctorField.TypeRef.OfType.Name)
}

func TestModuleIntrospectionJSON_Parseable(t *testing.T) {
	// Verify the JSON is valid and has __schema.
	funcs := buildTestFuncsForRoundTrip(t, "myMod")
	jsonBytes, err := funcs.ModuleIntrospectionJSON("myMod")
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(jsonBytes, &raw))
	_, hasSchema := raw["__schema"]
	require.True(t, hasSchema, "JSON must have __schema key")
}

// TestIntrospectArg_OptionalNonNullStripping verifies that an optional arg
// (pointer scalar or +optional) yields a nullable TypeRef (no NON_NULL wrapper),
// while a required arg remains NON_NULL.
func TestIntrospectArg_OptionalNonNullStripping(t *testing.T) {
	// Required string arg: TypeRef must be NON_NULL{SCALAR String}.
	required := paramSpec{
		name:     "req",
		typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
		optional: false,
	}
	reqIV := introspectArg(required)
	require.Equal(t, introspection.TypeKindNonNull, reqIV.TypeRef.Kind,
		"required arg must be NON_NULL")
	require.Equal(t, introspection.TypeKindScalar, reqIV.TypeRef.OfType.Kind)
	require.Equal(t, "String", reqIV.TypeRef.OfType.Name)

	// Optional string arg (optional flag set): TypeRef must be nullable SCALAR String.
	optByFlag := paramSpec{
		name:     "optFlag",
		typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: false},
		optional: true,
	}
	optFlagIV := introspectArg(optByFlag)
	require.Equal(t, introspection.TypeKindScalar, optFlagIV.TypeRef.Kind,
		"optional-by-flag arg must NOT be NON_NULL at the top level")
	require.Equal(t, "String", optFlagIV.TypeRef.Name)

	// Optional string arg (pointer type): parsedPrimitiveType with isPtr=true produces
	// a naked SCALAR, so isOptional() via isOptionalGoType / pointer detection still
	// results in a nullable ref (isPtr already produces a nullable ref).
	optByPtr := paramSpec{
		name:     "optPtr",
		typeSpec: &parsedPrimitiveType{goType: types.Typ[types.String], isPtr: true},
		optional: false,
	}
	optPtrIV := introspectArg(optByPtr)
	// isPtr=true on parsedPrimitiveType already produces SCALAR (no NON_NULL wrapper),
	// so the top-level kind must not be NON_NULL.
	require.NotEqual(t, introspection.TypeKindNonNull, optPtrIV.TypeRef.Kind,
		"pointer-typed arg must NOT be NON_NULL at the top level")
}

// TestIntrospectArg_PointerObjectEnumRequired verifies that a pointer object or
// enum arg WITHOUT explicit optionality stays required (NON_NULL), matching the
// engine's TypeDefCode (which only marks pointer *scalars* optional). A pointer
// alone must not make an object/enum arg optional.
func TestIntrospectArg_PointerObjectEnumRequired(t *testing.T) {
	objArg := introspectArg(paramSpec{
		name:     "dir",
		typeSpec: &parsedObjectTypeReference{name: "Directory", isPtr: true},
		optional: false,
	})
	require.Equal(t, introspection.TypeKindNonNull, objArg.TypeRef.Kind,
		"required pointer object arg must be NON_NULL")

	enumArg := introspectArg(paramSpec{
		name:     "color",
		typeSpec: &parsedEnumTypeReference{name: "Color", isPtr: true},
		optional: false,
	})
	require.Equal(t, introspection.TypeKindNonNull, enumArg.TypeRef.Kind,
		"required pointer enum arg must be NON_NULL")
	require.Equal(t, "Color", enumArg.TypeRef.OfType.Name)

	// With explicit +optional the same arg becomes nullable.
	optObjArg := introspectArg(paramSpec{
		name:     "dir",
		typeSpec: &parsedObjectTypeReference{name: "Directory", isPtr: true},
		optional: true,
	})
	require.NotEqual(t, introspection.TypeKindNonNull, optObjArg.TypeRef.Kind,
		"+optional pointer object arg must be nullable")
}

// TestIntrospectArg_ObjectsPassByID verifies object and interface args mirror
// the engine's TypeDef.ToInput: the schema type is an ID scalar carrying an
// @expectedType directive naming the target type, which the binding generator
// uses to recover the concrete parameter type.
func TestIntrospectArg_ObjectsPassByID(t *testing.T) {
	coreArg := introspectArg(paramSpec{
		name:     "dir",
		typeSpec: &parsedObjectTypeReference{name: "Directory", isPtr: true},
	})
	require.Equal(t, introspection.TypeKindNonNull, coreArg.TypeRef.Kind)
	require.Equal(t, introspection.TypeKindScalar, coreArg.TypeRef.OfType.Kind)
	require.Equal(t, "ID", coreArg.TypeRef.OfType.Name)
	require.Equal(t, "Directory", coreArg.Directives.ExpectedType())

	// A module-local object arg carries the namespaced expected type.
	localArg := introspectArg(paramSpec{
		name:     "box",
		typeSpec: &parsedObjectTypeReference{name: "Box", moduleName: "test", isPtr: true},
	})
	require.Equal(t, "ID", localArg.TypeRef.OfType.Name)
	require.Equal(t, "TestBox", localArg.Directives.ExpectedType())

	// A list of objects keeps the list shape with ID elements; the directive
	// names the element type (the engine walks list wrappers the same way).
	listArg := introspectArg(paramSpec{
		name: "boxes",
		typeSpec: &parsedSliceType{
			goType:     types.NewSlice(types.Typ[types.String]),
			underlying: &parsedObjectTypeReference{name: "Box", moduleName: "test", isPtr: true},
		},
	})
	require.Equal(t, introspection.TypeKindList, listArg.TypeRef.OfType.Kind)
	require.Equal(t, "ID", listArg.TypeRef.OfType.OfType.OfType.Name)
	require.Equal(t, "TestBox", listArg.Directives.ExpectedType())

	// Enums pass by value, not by ID.
	enumArg := introspectArg(paramSpec{
		name:     "color",
		typeSpec: &parsedEnumTypeReference{name: "Color", moduleName: "test"},
	})
	require.Equal(t, introspection.TypeKindEnum, enumArg.TypeRef.OfType.Kind)
	require.Empty(t, enumArg.Directives.ExpectedType())
}

// TestIntrospectArg_VariadicAndDefaultStayRequired verifies that args the
// engine registers as required keep their NON_NULL wrapper: variadic args, and
// args with only a +default (no +optional) — the engine carries the default in
// DefaultValue, not in nullability.
func TestIntrospectArg_VariadicAndDefaultStayRequired(t *testing.T) {
	variadic := introspectArg(paramSpec{
		name: "msgs",
		typeSpec: &parsedSliceType{
			goType:     types.NewSlice(types.Typ[types.String]),
			underlying: &parsedPrimitiveType{goType: types.Typ[types.String]},
		},
		variadic: true,
	})
	require.Equal(t, introspection.TypeKindNonNull, variadic.TypeRef.Kind,
		"variadic args are registered required by the engine")

	withDefault := introspectArg(paramSpec{
		name:            "greeting",
		typeSpec:        &parsedPrimitiveType{goType: types.Typ[types.String]},
		hasDefaultValue: true,
		defaultValue:    "hello",
	})
	require.Equal(t, introspection.TypeKindNonNull, withDefault.TypeRef.Kind,
		"+default without +optional stays NON_NULL, the default lives in DefaultValue")
	require.NotNil(t, withDefault.DefaultValue)
	require.Equal(t, `"hello"`, *withDefault.DefaultValue)
}

// TestIntrospectTypeRef_SliceOfObjects verifies that []*SomeObject is emitted as
// NON_NULL{ LIST{ NON_NULL{ OBJECT SomeObject } } } — a pointer element does not
// make the object nullable (object TypeDefCode never sets WithOptional).
func TestIntrospectTypeRef_SliceOfObjects(t *testing.T) {
	// Build a slice whose element is a pointer-to-object reference.
	elemRef := &parsedObjectTypeReference{name: "SomeObject", isPtr: true}
	ref := introspectTypeRef(&parsedSliceType{
		goType:     types.NewSlice(types.Typ[types.String]), // goType placeholder
		underlying: elemRef,
	})

	// Top: NON_NULL
	require.Equal(t, introspection.TypeKindNonNull, ref.Kind,
		"slice must be wrapped in NON_NULL")
	// Inside NON_NULL: LIST
	require.Equal(t, introspection.TypeKindList, ref.OfType.Kind,
		"inside NON_NULL must be LIST")
	// Inside LIST: NON_NULL{OBJECT} (a pointer does not make an object nullable)
	elem := ref.OfType.OfType
	require.Equal(t, introspection.TypeKindNonNull, elem.Kind,
		"list element must be NON_NULL (pointer does not make an object nullable)")
	require.Equal(t, introspection.TypeKindObject, elem.OfType.Kind)
	require.Equal(t, "SomeObject", elem.OfType.Name)
}

// TestIntrospectTypeRef_SliceOfNonNullObjects verifies that []SomeObject (non-pointer)
// is emitted as NON_NULL{ LIST{ NON_NULL{ OBJECT SomeObject } } }.
func TestIntrospectTypeRef_SliceOfNonNullObjects(t *testing.T) {
	elemRef := &parsedObjectTypeReference{name: "SomeObject", isPtr: false}
	ref := introspectTypeRef(&parsedSliceType{
		goType:     types.NewSlice(types.Typ[types.String]),
		underlying: elemRef,
	})

	require.Equal(t, introspection.TypeKindNonNull, ref.Kind)
	require.Equal(t, introspection.TypeKindList, ref.OfType.Kind)
	require.Equal(t, introspection.TypeKindNonNull, ref.OfType.OfType.Kind,
		"non-pointer element must be NON_NULL")
	require.Equal(t, introspection.TypeKindObject, ref.OfType.OfType.OfType.Kind)
	require.Equal(t, "SomeObject", ref.OfType.OfType.OfType.Name)
}

// TestIntrospectArg_EnumDefault validates Fix 1: an enum-typed arg with a default
// value emits the enum member *name* (JSON-quoted), not the raw Go const string.
func TestIntrospectArg_EnumDefault(t *testing.T) {
	// Construct an enum type reference with two members.
	// member "Active" has underlying value "ACTIVE".
	enumRef := &parsedEnumTypeReference{
		name: "Status",
		values: []*parsedEnumMember{
			{name: "Active", value: "ACTIVE"},
			{name: "Inactive", value: "INACTIVE"},
		},
		isPtr: false,
	}

	// Arg whose default is specified by the underlying value string "ACTIVE".
	argByValue := paramSpec{
		name:            "status",
		typeSpec:        enumRef,
		optional:        true,
		hasDefaultValue: true,
		defaultValue:    "ACTIVE", // raw pragma value
	}
	iv := introspectArg(argByValue)
	require.NotNil(t, iv.DefaultValue, "DefaultValue must be set for enum arg with default")
	// Expected: strconv.Quote("Active") == `"Active"`
	require.Equal(t, `"Active"`, *iv.DefaultValue,
		"enum default must resolve to the member name, JSON-quoted")

	// Arg whose default is specified by the member name directly.
	argByName := paramSpec{
		name:            "status2",
		typeSpec:        enumRef,
		optional:        true,
		hasDefaultValue: true,
		defaultValue:    "Active", // raw pragma value == member name
	}
	iv2 := introspectArg(argByName)
	require.NotNil(t, iv2.DefaultValue)
	require.Equal(t, `"Active"`, *iv2.DefaultValue,
		"enum default by name must also resolve to the member name, JSON-quoted")
}
