package templates

import (
	"testing"

	"github.com/iancoleman/strcase"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func sourceMapDirectives(module string) introspection.Directives {
	value := `"` + module + `"`
	return introspection.Directives{{
		Name: "sourceMap",
		Args: []*introspection.DirectiveArg{{Name: "module", Value: &value}},
	}}
}

func scalarRef(name string) *introspection.TypeRef {
	return &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: name}
}

func nonNull(ref *introspection.TypeRef) *introspection.TypeRef {
	return &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: ref}
}

// The schema names a module had before engine/naming are recovered from its
// current ones with the legacy strcase namer, as the engine names its
// transition aliases (see core/module_name_aliases_test.go).
func TestLegacyModuleSchemaNames(t *testing.T) {
	mod := &introspection.Type{Kind: introspection.TypeKindObject, Name: "DaggerCLI", Directives: sourceMapDirectives("dagger-cli")}
	field := func(name string) introspection.Field {
		return introspection.Field{Name: name, ParentObject: mod}
	}
	for name, legacy := range map[string]string{
		"getURL":       "getUrl",
		"makeSecretID": "makeSecretId",
		"baseURL":      "baseUrl",
		"legacyJSON":   "legacyJson",
		"daggerCLI":    "daggerCli",
		"plain":        "plain",
	} {
		require.Equal(t, legacy, legacyFieldSchemaName(field(name)), name)
	}
	// loadFooFromID: strcase's loadFooFromId was LoadFooFromID in Go too.
	require.Equal(t, "LoadFooFromID", legacyFormatName(legacyFieldSchemaName(field("loadFooFromID"))))

	arg := introspection.InputValue{Name: "legacyJSON"}
	require.Equal(t, "legacyJson", legacyArgSchemaName(field("fn"), arg))

	for _, c := range []struct{ name, module, legacy string }{
		{"DaggerCLI", "dagger-cli", "DaggerCli"},
		{"DaggerCLIHTTPClient", "dagger-cli", "DaggerCliHttpclient"},
		{"MyModThing", "my-mod", "MyModThing"},
		// A module named by strcase: its names are strcase's already.
		{"GoSdk", "go", "GoSdk"},
		{"DaggerCliHttpclient", "dagger-cli", "DaggerCliHttpclient"},
	} {
		typ := &introspection.Type{Kind: introspection.TypeKindObject, Name: c.name, Directives: sourceMapDirectives(c.module)}
		require.Equal(t, c.legacy, legacyTypeSchemaName(typ), c.name)
	}

	// Core names are written by hand: their schema names didn't change.
	core := &introspection.Type{Kind: introspection.TypeKindObject, Name: "GitCommit"}
	require.Equal(t, "parentShas", legacyFieldSchemaName(introspection.Field{Name: "parentShas", ParentObject: core}))
	require.Equal(t, "LLMContent", legacyTypeSchemaName(&introspection.Type{Kind: introspection.TypeKindObject, Name: "LLMContent"}))
	// ...unless a module adds them to a core type.
	require.Equal(t, "asDaggerCli", legacyFieldSchemaName(introspection.Field{
		Name: "asDaggerCLI", ParentObject: core, Directives: sourceMapDirectives("dagger-cli"),
	}))

	// Modules still named by strcase have strcase's names already: they get
	// no aliases, even where strcase doesn't leave its own output as is.
	for _, name := range []string{"getUrl", "daggerCli", "e2ETest", "withArm64", "x509Cert", "id"} {
		require.Equal(t, name, legacyFieldSchemaName(field(name)), name)
	}
	require.Equal(t, "e2Etest", strcase.ToLowerCamel("e2ETest"))
}

// legacyModuleSchema is a dependency module the new rules renamed: module
// dagger-cli (daggerCli, DaggerCli), with an argument they renamed too
// (commitSha is commitSHA).
func legacyModuleSchema(t *testing.T) *introspection.Schema {
	t.Helper()
	id := &introspection.Type{Kind: introspection.TypeKindScalar, Name: "ID"}
	str := &introspection.Type{Kind: introspection.TypeKindScalar, Name: "String"}
	cli := &introspection.Type{
		Kind:       introspection.TypeKindObject,
		Name:       "DaggerCLI",
		Directives: sourceMapDirectives("dagger-cli"),
		Fields: []*introspection.Field{
			{Name: "id", TypeRef: nonNull(scalarRef("ID"))},
			{
				Name:       "binary",
				TypeRef:    nonNull(scalarRef("String")),
				Directives: sourceMapDirectives("dagger-cli"),
				Args: introspection.InputValues{
					{Name: "platform", TypeRef: scalarRef("String")},
				},
			},
		},
	}
	mode := &introspection.Type{
		Kind:       introspection.TypeKindEnum,
		Name:       "DaggerCLIMode",
		Directives: sourceMapDirectives("dagger-cli"),
		EnumValues: []introspection.EnumValue{{Name: "FAST"}, {Name: "SLOW"}},
	}
	query := &introspection.Type{
		Kind: introspection.TypeKindObject,
		Name: "Query",
		Fields: []*introspection.Field{
			{
				Name:       "daggerCLI",
				TypeRef:    nonNull(&introspection.TypeRef{Kind: introspection.TypeKindObject, Name: "DaggerCLI"}),
				Directives: sourceMapDirectives("dagger-cli"),
				Args: introspection.InputValues{
					{Name: "version", TypeRef: scalarRef("String")},
					{Name: "commitSHA", TypeRef: scalarRef("String"), Directives: sourceMapDirectives("dagger-cli")},
				},
			},
		},
	}
	schema := &introspection.Schema{Types: introspection.Types{id, str, query, cli, mode}}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	return schema
}

// A dependency's old Go identifiers keep compiling, deprecated, where the new
// naming rules renamed its schema names (daggerCli is daggerCLI).
func TestLegacyModuleNames(t *testing.T) {
	schema := legacyModuleSchema(t)
	query := schema.Types.Get("Query")
	cli := schema.Types.Get("DaggerCLI")

	got := renderTemplate(t, parseTemplateFiles(t, schema, "_types/object_fields.go.tmpl"), query)
	require.Contains(t, got, "func (r *Query) DaggerCLI(opts ...DaggerCLIOpts) *DaggerCLI")
	require.Contains(t, got, "// Deprecated: use DaggerCLI instead.\nfunc (r *Query) DaggerCli(opts ...DaggerCLIOpts) *DaggerCLI {\n\treturn r.DaggerCLI(opts...)\n}")
	require.Contains(t, got, "// Deprecated: use DaggerCLIOpts instead.\ntype DaggerCliOpts = DaggerCLIOpts")
	require.Regexp(t, `CommitSHA string[^\n]*\n\s+// Deprecated: use CommitSHA instead.\n\s+CommitSha string`, got)
	require.Contains(t, got, `if querybuilder.IsZeroValue(opts[i].CommitSHA) && !querybuilder.IsZeroValue(opts[i].CommitSha) {
			q = q.Arg("commitSHA", opts[i].CommitSha)`)
	// The wire names don't change.
	require.Contains(t, got, `q := r.query.Select("daggerCLI")`)
	require.NotContains(t, got, `"daggerCli"`)

	got = renderTemplate(t, parseTemplateFiles(t, schema, "_types/object.go.tmpl"), cli)
	require.Contains(t, got, "// Deprecated: use DaggerCLI instead.\ntype DaggerCli = DaggerCLI")
	require.Contains(t, got, "// Deprecated: use DaggerCLIBinaryOpts instead.\ntype DaggerCliBinaryOpts = DaggerCLIBinaryOpts")

	got = renderTemplate(t, parseTemplateFiles(t, schema, "_types/enum.go.tmpl"), schema.Types.Get("DaggerCLIMode"))
	require.Contains(t, got, "// Deprecated: use DaggerCLIMode instead.\ntype DaggerCliMode = DaggerCLIMode")
	require.Contains(t, got, "// Deprecated: use DaggerCLIModeFast instead.\n\tDaggerCliModeFast DaggerCLIMode = DaggerCLIModeFast")
}

// Core declarations get no aliases: their schema names didn't change.
func TestLegacyNamesSkipCore(t *testing.T) {
	commit := &introspection.Type{
		Kind: introspection.TypeKindObject,
		Name: "GitCommit",
		Fields: []*introspection.Field{
			{Name: "parentShas", TypeRef: nonNull(scalarRef("String"))},
			{
				Name:    "shortSha",
				TypeRef: nonNull(scalarRef("String")),
				Args:    introspection.InputValues{{Name: "minLength", TypeRef: scalarRef("Int")}},
			},
		},
	}
	schema := &introspection.Schema{Types: introspection.Types{commit}}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	funcs := goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions("", &FormatTypeFunc{}),
		fullSchema:      schema,
		schema:          schema,
	}
	require.Empty(t, funcs.legacyTypeName(commit))
	for _, f := range commit.Fields {
		_, ok := legacyMethodName(*f)
		require.False(t, ok, f.Name)
		require.Empty(t, funcs.legacyFieldOptionsStructName(*f), f.Name)
		for _, arg := range f.Args {
			require.Empty(t, funcs.legacyOptionalArgName(*f, arg))
		}
	}
}
