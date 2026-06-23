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

	// Split dependency-contributed types into their own <dep>.gen.ts files.
	// The core file (client.gen.ts) is rendered from a schema with the
	// dep-owned types removed; for the extendable types (Query/Binding/Env)
	// the dep-contributed fields are dropped and re-attached as prototype
	// augmentations in each per-dep file. With no dependencies this is a
	// no-op and client.gen.ts retains its full-schema contents.
	// Only split *dependencies* into their own files; the module being
	// generated for keeps its own types in client.gen.ts.
	selfModule := selfModuleName(config)
	depNames := dependencyModules(schema, selfModule)
	coreSchema := schema
	if len(depNames) > 0 {
		coreSchema = schema.Exclude(depNames...)
	}

	// The template funcs always get the full schema so the dependency-
	// splitting helpers can enumerate deps regardless of which (possibly
	// filtered) schema a given file is rendered from.
	tmpl := templates.New(schemaVersion, schema, selfModule, config)

	mfs := memfs.New()

	// Render the core file (client.gen.ts) from the filtered core schema.
	if err := renderTemplate(mfs, tmpl, "api", target, depFileData{
		Schema:        coreSchema,
		SchemaVersion: schemaVersion,
		Types:         coreSchema.Types,
	}); err != nil {
		return nil, err
	}

	// Render one <dep>.gen.ts file per dependency.
	for _, depName := range depNames {
		depSchema := schema.Include(depName)
		depTarget := filepath.Join(filepath.Dir(target), strcase.ToKebab(depName)+".gen.ts")
		if err := renderTemplate(mfs, tmpl, "dep", depTarget, depFileData{
			Schema:        depSchema,
			SchemaVersion: schemaVersion,
			Types:         depSchema.Types,
			DepName:       depName,
		}); err != nil {
			return nil, fmt.Errorf("render dependency %q: %w", depName, err)
		}
	}

	return &generator.GeneratedState{
		Overlay: mfs,
	}, nil
}

// selfModuleName returns the name of the module the client is generated for
// (from the module or client config), or "" when generating outside a module
// (e.g. the SDK's own library client).
func selfModuleName(config generator.Config) string {
	if config.ModuleConfig != nil {
		return config.ModuleConfig.ModuleName
	}
	if config.ClientConfig != nil {
		return config.ClientConfig.ModuleName
	}
	return ""
}

// dependencyModules returns the schema's module names with the module being
// generated for (self) removed: only dependencies are split into their own
// files. Names are compared kebab-cased to tolerate casing differences between
// sourceMap module names and the configured name.
func dependencyModules(schema *introspection.Schema, self string) []string {
	all := schema.DependencyNames()
	out := make([]string, 0, len(all))
	for _, name := range all {
		if self != "" && strcase.ToKebab(name) == strcase.ToKebab(self) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// depFileData is the template "dot" for both the core "api" template and the
// per-dependency "dep" template. DepName is only set for dep files and is used
// to derive a unique augmentation function name.
type depFileData struct {
	Schema        *introspection.Schema
	SchemaVersion string
	Types         []*introspection.Type
	DepName       string
}

// renderTemplate executes the named template against data and writes the
// result to target inside mfs.
func renderTemplate(mfs *memfs.FS, tmpl *template.Template, name, target string, data depFileData) error {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return fmt.Errorf("render %q: %w", name, err)
	}
	if err := mfs.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", filepath.Dir(target), err)
	}
	if err := mfs.WriteFile(target, b.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed to write client file at %s: %w", target, err)
	}
	return nil
}
