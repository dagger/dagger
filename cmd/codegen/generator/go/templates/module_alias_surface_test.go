package templates

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// TestModuleAliasSurfaceMatchesCore guards the split between the real module
// bindings (internal/dagger/core) and the alias layer that re-exports them
// (internal/dagger). The alias template re-derives, per type, whether core
// declares an options struct, a With<T>Func, a <T>Client or unscoped enum
// constants — all of which are version-gated in the type templates. Any drift
// between the two produces a reference to a symbol core never declared, which
// only shows up when a real module is regenerated and compiled.
//
// Rather than assert on specific symbols, this walks every core.X reference
// the alias output makes and requires a matching top-level declaration in the
// core output, across the schema views whose gates differ.
func TestModuleAliasSurfaceMatchesCore(t *testing.T) {
	for _, schemaVersion := range []string{
		"v0.21.0-dev", // post-cutover: interface clients, scoped enums
		"v0.20.6",     // legacy interfaces, scoped enums
		"v0.14.0",     // legacy interfaces, unscoped enum aliases
		"v0.11.0",     // also predates the optional-argument cutover
	} {
		t.Run(schemaVersion, func(t *testing.T) {
			schema := aliasSurfaceSchema()
			generator.SetSchemaParents(schema)
			generator.SetSchema(schema)
			t.Cleanup(func() { generator.SetSchema(nil) })

			funcs := GoTemplateFuncs(t.Context(), schema, schema, schemaVersion, generator.Config{
				ModuleConfig: &generator.ModuleGeneratorConfig{ModuleName: "test"},
			}, nil, nil, 0)
			tree, err := buildTemplateTree(funcs)
			require.NoError(t, err)

			data := struct {
				PackageName   string
				PackageImport string
				Schema        *introspection.Schema
				SchemaVersion string
				Types         []*introspection.Type
			}{
				PackageName:   "dagger",
				PackageImport: "dagger/test",
				Schema:        schema,
				SchemaVersion: schemaVersion,
				Types:         schema.Visit(),
			}

			render := func(name string) string {
				tmpl := tree.Lookup(name)
				require.NotNil(t, tmpl, "template %q not found", name)
				var buf bytes.Buffer
				require.NoError(t, tmpl.Execute(&buf, data))
				return buf.String()
			}

			coreSrc := render("internal/dagger/core/core.gen.go.tmpl")
			aliasSrc := render("internal/dagger/dagger.gen.go.tmpl")

			declared := topLevelDecls(t, "core.gen.go", coreSrc)
			for _, ref := range qualifiedRefs(t, "dagger.gen.go", aliasSrc, "core") {
				require.Contains(t, declared, ref,
					"internal/dagger references core.%s, which internal/dagger/core does not declare", ref)
			}
		})
	}
}

// aliasSurfaceSchema builds a schema exercising each construct the alias
// template special-cases: a self-chainable object (With<T>Func), a field with
// optional arguments (options struct), an interface (<T>Client), and an enum
// with more than one value (scoped and unscoped constants).
func aliasSurfaceSchema() *introspection.Schema {
	idRef := &introspection.TypeRef{
		Kind:   introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "ID"},
	}
	fooRef := &introspection.TypeRef{
		Kind:   introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{Kind: introspection.TypeKindObject, Name: "Foo"},
	}
	optionalString := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}

	id := &introspection.Type{Kind: introspection.TypeKindScalar, Name: "ID"}
	foo := &introspection.Type{
		Kind: introspection.TypeKindObject,
		Name: "Foo",
		Fields: []*introspection.Field{
			{Name: "id", TypeRef: idRef},
			{
				Name:    "withBar",
				TypeRef: fooRef,
				Args:    introspection.InputValues{{Name: "bar", TypeRef: optionalString}},
			},
		},
	}
	query := &introspection.Type{
		Kind: introspection.TypeKindObject,
		Name: "Query",
		Fields: []*introspection.Field{
			{
				Name:    "foo",
				TypeRef: fooRef,
				Args:    introspection.InputValues{{Name: "opt", TypeRef: optionalString}},
			},
		},
	}
	iface := &introspection.Type{
		Kind: introspection.TypeKindInterface,
		Name: "Baz",
		Fields: []*introspection.Field{
			{Name: "id", TypeRef: idRef},
		},
	}
	enum := &introspection.Type{
		Kind:       introspection.TypeKindEnum,
		Name:       "Mood",
		EnumValues: []introspection.EnumValue{{Name: "HAPPY"}, {Name: "SAD"}},
	}

	return &introspection.Schema{Types: introspection.Types{id, query, foo, iface, enum}}
}

// topLevelDecls returns the set of package-level names src declares.
func topLevelDecls(t *testing.T, filename, src string) map[string]struct{} {
	t.Helper()

	file := parseGo(t, filename, src)
	decls := map[string]struct{}{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				decls[d.Name.Name] = struct{}{}
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					decls[s.Name.Name] = struct{}{}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						decls[name.Name] = struct{}{}
					}
				}
			}
		}
	}
	return decls
}

// qualifiedRefs returns every selector name src reads off the given package
// qualifier, e.g. "Container" for a reference to core.Container.
func qualifiedRefs(t *testing.T, filename, src, qualifier string) []string {
	t.Helper()

	file := parseGo(t, filename, src)
	seen := map[string]struct{}{}
	var refs []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != qualifier {
			return true
		}
		if _, dup := seen[sel.Sel.Name]; dup {
			return true
		}
		seen[sel.Sel.Name] = struct{}{}
		refs = append(refs, sel.Sel.Name)
		return true
	})
	return refs
}

func parseGo(t *testing.T, filename, src string) *ast.File {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	require.NoError(t, err, "generated %s does not parse:\n%s", filename, src)
	return file
}
