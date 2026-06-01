package typescriptgenerator

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// TestDepTemplate_RendersDepTypes renders the per-dep template against a small
// hand-crafted schema and asserts:
//   - dep-owned scalar / class are emitted;
//   - extendable types (Query/Client, Binding, Env) become
//     `declare module` + prototype-assignment blocks (Phase 2);
//   - the dep file imports BaseClient from the SDK runtime (not from
//     client.gen.ts — would create an ESM cycle).
func TestDepTemplate_RendersDepTypes(t *testing.T) {
	helloModule := newSourceMapDirective("hello")

	full := &introspection.Schema{
		QueryType: struct {
			Name string `json:"name,omitempty"`
		}{Name: "Query"},
		Types: introspection.Types{
			// Extendable type with one dep-contributed field.
			{
				Kind: introspection.TypeKindObject,
				Name: "Query",
				Fields: []*introspection.Field{
					{
						Name: "hello",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindObject,
								Name: "Hello",
							},
						},
						Directives: introspection.Directives{helloModule},
					},
				},
			},
			// Dep-owned scalar.
			{
				Kind:        introspection.TypeKindScalar,
				Name:        "HelloID",
				Description: "Hello identifier.",
				Directives:  introspection.Directives{helloModule},
			},
			// Dep-owned regular class.
			{
				Kind:       introspection.TypeKindObject,
				Name:       "Hello",
				Directives: introspection.Directives{helloModule},
				Fields: []*introspection.Field{
					{
						Name: "greet",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "String",
							},
						},
					},
				},
			},
			// Core type the dep depends on conceptually. Not emitted in the
			// dep file itself; included so CoreTypeNames returns it.
			{
				Kind: introspection.TypeKindObject,
				Name: "Container",
			},
		},
	}
	generator.SetSchemaParents(full)

	depSchema := full.Include("hello")
	generator.SetSchemaParents(depSchema)

	tmpl := templates.New("v0.21.0", full, generator.Config{
		Lang: generator.SDKLangTypeScript,
		ModuleConfig: &generator.ModuleGeneratorConfig{
			ModuleName:       "host",
			ModuleSourcePath: ".",
		},
	})

	out := renderDepTemplate(t, tmpl, depSchema, "hello")

	// dep-owned scalar and class must appear.
	require.Contains(t, out, "HelloID",
		"dep-owned scalar must be emitted in the dep file")
	require.Contains(t, out, "export class Hello extends BaseClient",
		"dep-owned class must be emitted in the dep file")

	// extendable types (Query/Client, Binding, Env) must NOT be re-declared
	// as classes — they're augmented via declare-module + prototype.
	require.NotContains(t, out, "export class Client extends BaseClient",
		"extendable type Client must not be re-rendered in the dep file")

	// Phase 2: dep-contributed extendable-type fields become augmentations.
	require.Contains(t, out, `declare module "./client.gen.js"`,
		"dep file must declare-module merge into client.gen.ts for IDE completion")
	require.Contains(t, out, "interface Client {",
		"dep-contributed Client methods must be declared via interface merging")
	require.Contains(t, out, "Client.prototype.hello",
		"dep-contributed methods must be attached via prototype assignment so they work at runtime")

	// BaseClient comes from the SDK runtime, not from client.gen.ts.
	require.Contains(t, out, "BaseClient",
		"dep file must import BaseClient so its classes can extend it")
	require.Regexp(t, `import\s*\{\s*Context,\s*BaseClient`, out,
		"BaseClient must be imported alongside Context from the runtime")

	// Other core types are imported type-only from client.gen.ts — type-only
	// imports are erased at runtime, so no ESM cycle.
	require.Contains(t, out, `import type {`)
	require.Contains(t, out, `from "./client.gen.js"`)
}

// TestHeaderTemplate_EmitsDependencyExports renders the header template
// against a schema containing two deps and asserts that one `export *` line
// is emitted per dep, with kebab-cased filenames.
func TestHeaderTemplate_EmitsDependencyExports(t *testing.T) {
	full := &introspection.Schema{
		QueryType: struct {
			Name string `json:"name,omitempty"`
		}{Name: "Query"},
		Types: introspection.Types{
			newType("Hello", introspection.TypeKindObject,
				introspection.Directives{newSourceMapDirective("hello")}),
			newType("MyDep", introspection.TypeKindObject,
				introspection.Directives{newSourceMapDirective("myDep")}),
		},
	}

	tmpl := templates.New("v0.21.0", full, generator.Config{
		Lang: generator.SDKLangTypeScript,
		ModuleConfig: &generator.ModuleGeneratorConfig{
			ModuleName:       "host",
			ModuleSourcePath: ".",
		},
	})

	var b bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&b, "header", nil))
	out := b.String()

	require.Contains(t, out, "export { BaseClient }",
		"client.gen.ts must re-export BaseClient (the class is defined in the SDK runtime to avoid an ESM cycle with dep files)")
	require.Contains(t, out, `export * from "./hello.gen.js"`)
	require.Contains(t, out, `export * from "./my-dep.gen.js"`,
		"camelCase dep names must be kebab-cased in the filename")
}

func renderDepTemplate(t *testing.T, tmpl *template.Template, schema *introspection.Schema, depName string) string {
	t.Helper()
	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
		DepName       string
	}{
		Schema:        schema,
		SchemaVersion: "v0.21.0",
		Types:         schema.Types,
		DepName:       depName,
	}
	var b bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&b, "dep", data))
	return b.String()
}

func newType(name string, kind introspection.TypeKind, directives introspection.Directives) *introspection.Type {
	return &introspection.Type{
		Kind:       kind,
		Name:       name,
		Directives: directives,
	}
}

func newSourceMapDirective(moduleName string) *introspection.Directive {
	v := `"` + moduleName + `"`
	return &introspection.Directive{
		Name: "sourceMap",
		Args: []*introspection.DirectiveArg{
			{Name: "module", Value: &v},
		},
	}
}
