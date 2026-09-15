package templates

import (
	"embed"
	"fmt"
	"text/template"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// all: is required so the underscore-prefixed helper templates
// (e.g. _dep.ts.gtpl) are embedded — the default //go:embed skips files
// whose names begin with "_" or ".".
//
//go:embed all:src
var srcs embed.FS

// New creates a new template with all the template dependencies set up.
//
// fullSchema is the complete (unfiltered) schema; it is passed to the
// template funcs so the dependency-splitting helpers can enumerate deps even
// when an individual file is rendered from a filtered schema. selfModule is the
// name of the module being generated for (its own types stay in client.gen.ts;
// only dependencies are split out).
func New(
	schemaVersion string,
	fullSchema *introspection.Schema,
	selfModule string,
	cfg generator.Config,
) *template.Template {
	topLevelTemplate := "api"
	templateDeps := []string{
		topLevelTemplate, "header", "objects", "object", "interface", "method", "method_solve", "call_args", "method_comment", "types", "args", "default",
		// Dependency-splitting templates: the per-dep file ("dep"), the
		// prototype augmentations, and the shared method bodies reused by both
		// the class-field methods and the augmentation prototype methods.
		"_dep", "_augmentations", "_method_body", "_method_solve_body",
	}

	fileNames := make([]string, 0, len(templateDeps))
	for _, tmpl := range templateDeps {
		fileNames = append(fileNames, fmt.Sprintf("src/%s.ts.gtpl", tmpl))
	}

	funcs := TypescriptTemplateFuncs(schemaVersion, fullSchema, selfModule, cfg)
	tmpl := template.Must(template.New(topLevelTemplate).Funcs(funcs).ParseFS(srcs, fileNames...))
	return tmpl
}

// NewEntrypoint creates a template that renders the static dispatch
// `__dagger.entrypoint.ts` file. The templates live under
// src/entrypoint/*.gtpl and consume the typedef JSON shape produced by the
// SDK introspector.
func NewEntrypoint(module *TypedefModule, opts EntrypointOptions) *template.Template {
	return template.Must(
		template.New("entrypoint").
			Funcs(EntrypointTemplateFuncs(module, opts)).
			ParseFS(srcs, "src/entrypoint/*.gtpl"),
	)
}

// EntrypointOptions controls how user-source imports and SDK references are
// rendered in the generated entrypoint.
type EntrypointOptions struct {
	// SDKImportPath is the bare specifier the entrypoint uses to import
	// runtime helpers (defaults to "@dagger.io/dagger").
	SDKImportPath string

	// ModuleRoot is the absolute path of the user's module root, used to
	// resolve relative TS import paths for each registered @object class.
	ModuleRoot string

	// SourceDir is the user's source directory name relative to ModuleRoot
	// (defaults to "src").
	SourceDir string
}
