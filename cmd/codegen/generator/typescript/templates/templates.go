package templates

import (
	"embed"
	"text/template"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

//go:embed all:src
var srcs embed.FS

// New creates a new template with all the template dependencies set up.
//
// All *.ts.gtpl files directly under src/ are parsed and registered. Files
// whose basename starts with "_" are treated as helper templates: they are
// parsed (so they can be referenced via {{ template "name" . }}) but their
// existence is otherwise opaque to the generator, which selects the
// top-level template by name (e.g. "api" or "dep").
func New(
	schemaVersion string,
	fullSchema *introspection.Schema,
	cfg generator.Config,
) *template.Template {
	funcs := TypescriptTemplateFuncs(schemaVersion, fullSchema, cfg)
	return template.Must(template.New("typescript").Funcs(funcs).ParseFS(srcs, "src/*.ts.gtpl"))
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
