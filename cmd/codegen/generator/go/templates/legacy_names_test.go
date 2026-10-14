package templates

import (
	"testing"

	"github.com/iancoleman/strcase"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/internal/testnames"
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
	// ...unless they were renamed since: then they had their old names.
	require.Equal(t, "parentShas", legacyFieldSchemaName(introspection.Field{Name: "parentSHAs", ParentObject: core}))
	withRemote := introspection.Field{Name: "withRemote", ParentObject: &introspection.Type{Kind: introspection.TypeKindObject, Name: "GitRepository"}}
	require.Equal(t, "pushUrl", legacyArgSchemaName(withRemote, introspection.InputValue{Name: "pushURL"}))
	require.Equal(t, "url", legacyArgSchemaName(withRemote, introspection.InputValue{Name: "url"}))
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
// dagger-cli (daggerCli, DaggerCli), next to one they named the same way (go,
// whose Go identifiers only changed formatting: GoVcs, vcsCommit).
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
	goVCS := &introspection.Type{
		Kind:       introspection.TypeKindObject,
		Name:       "GoVcs",
		Directives: sourceMapDirectives("go"),
		Fields: []*introspection.Field{
			{Name: "id", TypeRef: nonNull(scalarRef("ID"))},
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
					{Name: "vcsCommit", TypeRef: scalarRef("String"), Directives: sourceMapDirectives("dagger-cli")},
				},
			},
			{
				Name:       "go",
				TypeRef:    nonNull(&introspection.TypeRef{Kind: introspection.TypeKindObject, Name: "GoVcs"}),
				Directives: sourceMapDirectives("go"),
				Args: introspection.InputValues{
					{Name: "vcsCommit", TypeRef: scalarRef("String"), Directives: sourceMapDirectives("go")},
				},
			},
		},
	}
	schema := &introspection.Schema{Types: introspection.Types{id, str, query, cli, goVCS, mode}}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	return schema
}

// A dependency's old Go identifiers keep compiling, deprecated: those the
// new naming rules renamed in the schema (daggerCli is daggerCLI), and those
// the engine's formatting renamed in Go (VcsCommit is VCSCommit).
func TestLegacyModuleNames(t *testing.T) {
	schema := legacyModuleSchema(t)
	testnames.Load(schema, NameFormats...)
	query := schema.Types.Get("Query")
	cli := schema.Types.Get("DaggerCLI")

	got := renderTemplate(t, parseTemplateFiles(t, schema, "_types/object_fields.go.tmpl"), query)
	require.Contains(t, got, "func (r *Query) DaggerCLI(opts ...DaggerCLIOpts) *DaggerCLI")
	require.Contains(t, got, "// Deprecated: use DaggerCLI instead.\nfunc (r *Query) DaggerCli(opts ...DaggerCLIOpts) *DaggerCLI {\n\treturn r.DaggerCLI(opts...)\n}")
	require.Contains(t, got, "// Deprecated: use DaggerCLIOpts instead.\ntype DaggerCliOpts = DaggerCLIOpts")
	require.Regexp(t, `VCSCommit string[^\n]*\n\s+// Deprecated: use VCSCommit instead.\n\s+VcsCommit string`, got)
	require.Contains(t, got, `if querybuilder.IsZeroValue(opts[i].VCSCommit) && !querybuilder.IsZeroValue(opts[i].VcsCommit) {
			q = q.Arg("vcsCommit", opts[i].VcsCommit)`)
	// The wire names don't change.
	require.Contains(t, got, `q := r.query.Select("daggerCLI")`)
	require.NotContains(t, got, `"daggerCli"`)

	got = renderTemplate(t, parseTemplateFiles(t, schema, "_types/object.go.tmpl"), cli)
	require.Contains(t, got, "// Deprecated: use DaggerCLI instead.\ntype DaggerCli = DaggerCLI")
	require.Contains(t, got, "// Deprecated: use DaggerCLIBinaryOpts instead.\ntype DaggerCliBinaryOpts = DaggerCLIBinaryOpts")

	got = renderTemplate(t, parseTemplateFiles(t, schema, "_types/object.go.tmpl"), schema.Types.Get("GoVcs"))
	require.Contains(t, got, "// Deprecated: use GoVCS instead.\ntype GoVcs = GoVCS")

	got = renderTemplate(t, parseTemplateFiles(t, schema, "_types/enum.go.tmpl"), schema.Types.Get("DaggerCLIMode"))
	require.Contains(t, got, "// Deprecated: use DaggerCLIMode instead.\ntype DaggerCliMode = DaggerCLIMode")
	require.Contains(t, got, "// Deprecated: use DaggerCLIModeFast instead.\n\tDaggerCliModeFast DaggerCLIMode = DaggerCLIModeFast")

	want := updateAndGetFixture(t, "testdata/legacy_module_names.golden", renderTemplate(t, parseTemplateFiles(t, schema, "_types/object_fields.go.tmpl"), query))
	require.Equal(t, want, renderTemplate(t, parseTemplateFiles(t, schema, "_types/object_fields.go.tmpl"), query))
}

// Core declarations get no new aliases: their schema names didn't change,
// and neither did the Go names of their types or option struct fields.
func TestLegacyNamesSkipCore(t *testing.T) {
	schema, object := loadSchemaFromTypeJSON(t, engineNamesObjectJSON)
	testnames.Load(schema, NameFormats...)
	funcs := goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions("", &FormatTypeFunc{}),
		fullSchema:      schema,
		schema:          schema,
	}
	require.Empty(t, funcs.legacyTypeName(object))
	for _, f := range object.Fields {
		for _, arg := range f.Args {
			require.Empty(t, funcs.legacyOptionalArgName(*f, arg))
		}
	}
}
