package typescriptgenerator

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"text/template"

	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const ClientGenFile = "client.gen.ts"

type TypeScriptGenerator struct {
	Config generator.Config
}

// Generate will generate the TypeScript SDK code and might modify the schema to reorder types in a alphanumeric fashion.
func (g *TypeScriptGenerator) GenerateModule(_ context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	target := filepath.Join(g.Config.ModuleConfig.ModuleSourcePath, "sdk/src/api", ClientGenFile)

	return generate(g.Config, target, schema, schemaVersion)
}

func (g *TypeScriptGenerator) GenerateClient(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	return generate(g.Config, ClientGenFile, schema, schemaVersion)
}

func (g *TypeScriptGenerator) GenerateLibrary(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	return generate(g.Config, ClientGenFile, schema, schemaVersion)
}

func (g *TypeScriptGenerator) GenerateTypeDefs(_ context.Context, _ *introspection.Schema, _ string) (*generator.GeneratedState, error) {
	return nil, fmt.Errorf("not implemented for %s SDK", generator.SDKLangTypeScript)
}

func generate(config generator.Config, target string, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)
	sortSchema(schema)

	// Enumerate dependency module names so we can split them into per-dep
	// files. When there are no deps, this is a no-op and the core file
	// retains its previous (full-schema) contents.
	//
	// Phase 2: Schema.Exclude(deps...) also drops dep-contributed fields
	// from the extendable types (Query/Binding/Env); those fields are
	// emitted as prototype augmentations in each <dep>.gen.ts file.
	depNames := schema.DependencyNames()
	coreSchema := schema
	if len(depNames) > 0 {
		coreSchema = schema.Exclude(depNames...)
	}

	tmpl := templates.New(schemaVersion, schema, config)

	mfs := memfs.New()

	// Render the core file from the (possibly filtered) core schema.
	if err := renderToFile(mfs, tmpl, "api", target, coreSchema, schemaVersion); err != nil {
		return nil, err
	}

	// Render one file per dependency.
	for _, depName := range depNames {
		depSchema := schema.Include(depName)
		depTarget := filepath.Join(filepath.Dir(target), strcase.ToKebab(depName)+".gen.ts")
		if err := renderDepFile(mfs, tmpl, depTarget, depSchema, schemaVersion, depName); err != nil {
			return nil, fmt.Errorf("render dependency %q: %w", depName, err)
		}
	}

	return &generator.GeneratedState{
		Overlay: mfs,
	}, nil
}

// renderToFile executes the named template against the given schema and
// writes the resulting bytes to target inside mfs.
func renderToFile(
	mfs *memfs.FS,
	tmpl *template.Template,
	tmplName string,
	target string,
	schema *introspection.Schema,
	schemaVersion string,
) error {
	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
	}{
		Schema:        schema,
		SchemaVersion: schemaVersion,
		Types:         schema.Types,
	}
	return writeRendered(mfs, tmpl, tmplName, target, data)
}

// renderDepFile renders the per-dep "dep" template; the data carries an
// extra DepName field so the template can derive a unique augmentation
// function name (e.g. `__applyHelloAugmentations`).
func renderDepFile(
	mfs *memfs.FS,
	tmpl *template.Template,
	target string,
	depSchema *introspection.Schema,
	schemaVersion string,
	depName string,
) error {
	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
		DepName       string
	}{
		Schema:        depSchema,
		SchemaVersion: schemaVersion,
		Types:         depSchema.Types,
		DepName:       depName,
	}
	return writeRendered(mfs, tmpl, "dep", target, data)
}

func writeRendered(
	mfs *memfs.FS,
	tmpl *template.Template,
	tmplName string,
	target string,
	data any,
) error {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, tmplName, data); err != nil {
		return fmt.Errorf("render %q: %w", tmplName, err)
	}
	if err := mfs.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fmt.Errorf("create directory %s: %w", filepath.Dir(target), err)
	}
	if err := mfs.WriteFile(target, b.Bytes(), 0600); err != nil {
		return fmt.Errorf("write file %s: %w", target, err)
	}
	return nil
}

func sortSchema(schema *introspection.Schema) {
	sort.SliceStable(schema.Types, func(i, j int) bool {
		return schema.Types[i].Name < schema.Types[j].Name
	})
	for _, v := range schema.Types {
		sort.SliceStable(v.Fields, func(i, j int) bool {
			in := v.Fields[i].Name
			jn := v.Fields[j].Name
			switch {
			case in == "id" && jn == "id":
				return false
			case in == "id":
				return true
			case jn == "id":
				return false
			default:
				return in < jn
			}
		})
	}
}

