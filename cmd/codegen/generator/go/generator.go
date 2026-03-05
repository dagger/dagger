package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const (
	// ClientGenFile is the path to write the codegen for the dagger API
	ClientGenFile = "dagger.gen.go"

	// StarterTemplateFile is the path to write the default module code
	StarterTemplateFile = "main.go"

	// internalDaggerDir is the directory where internal dagger generated files are written.
	internalDaggerDir = "internal/dagger"
)

var goVersion = strings.TrimPrefix(runtime.Version(), "go")

type GoGenerator struct {
	Config generator.Config
}

// fullSchemaTemplates is the set of output file paths (without .tmpl suffix)
// that should be rendered against the full schema rather than the core schema
// (i.e. the schema with dependency types excluded). These templates need
// visibility into dep-contributed Query fields (e.g. hello(), loadHelloFromID())
// so that they can expose those constructors to callers.
var fullSchemaTemplates = map[string]bool{
	"dag/dag.gen.go": true,
}

func generateCode(
	ctx context.Context,
	cfg generator.Config,
	schema *introspection.Schema,
	schemaVersion string,
	mfs *memfs.FS,
	pkgInfo *PackageInfo,
	pkg *packages.Package,
	fset *token.FileSet,
	pass int,
) error {
	// Collect all dependency module names present in the schema so we can
	// split them out into separate files and exclude them from the main
	// internal/dagger/dagger.gen.go.
	depNames := schema.DependencyNames()

	// When there are dependencies, generate the core schema (excluding all
	// deps) into the main dagger.gen.go, then generate each dependency into
	// its own file.
	coreSchema := schema
	if len(depNames) > 0 {
		coreSchema = schema.Exclude(depNames...)
	}

	// Build two template sets: one bound to the core schema (most files) and
	// one bound to the full schema (dag/dag.gen.go and other files that need
	// to expose dep-contributed Query fields).
	coreFuncs := templates.GoTemplateFuncs(ctx, coreSchema, schema, schemaVersion, cfg, pkg, fset, pass)
	fullFuncs := templates.GoTemplateFuncs(ctx, schema, schema, schemaVersion, cfg, pkg, fset, pass)

	coreTmpls := templates.Templates(coreFuncs)
	fullTmpls := templates.Templates(fullFuncs)

	// Sort template keys for deterministic processing
	keys := make([]string, 0, len(coreTmpls))
	for k := range coreTmpls {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		// Choose the right template + schema pair for this output file.
		renderSchema := coreSchema
		tmpl := coreTmpls[k]
		if fullSchemaTemplates[k] {
			renderSchema = schema
			tmpl = fullTmpls[k]
		}

		dt, err := renderFile(cfg.OutputDir, renderSchema, schemaVersion, pkgInfo, tmpl)
		if err != nil {
			return err
		}
		if dt == nil {
			// no contents, skip
			continue
		}

		// Special case for client generation, we want to write the file in the specified client directory.
		if cfg.ClientConfig != nil && cfg.ClientConfig.ClientDir != "" {
			if err := mfs.MkdirAll(filepath.Join(cfg.ClientConfig.ClientDir, filepath.Dir(k)), 0o755); err != nil {
				return err
			}
			if err := mfs.WriteFile(filepath.Join(cfg.ClientConfig.ClientDir, k), dt, 0600); err != nil {
				return err
			}

			continue
		}

		if err := mfs.MkdirAll(filepath.Dir(k), 0o755); err != nil {
			return err
		}
		if err := mfs.WriteFile(k, dt, 0600); err != nil {
			return err
		}
	}

	// Generate per-dependency files.
	if len(depNames) > 0 {
		if err := generateDependencyFiles(ctx, cfg, schema, schemaVersion, mfs, pkgInfo, pkg, fset, pass, depNames); err != nil {
			return fmt.Errorf("generate dependency files: %w", err)
		}
	}

	return nil
}

// generateDependencyFiles generates one <dep>.gen.go file per dependency inside
// internal/dagger/, each containing only the types contributed by that dependency.
func generateDependencyFiles(
	ctx context.Context,
	cfg generator.Config,
	schema *introspection.Schema,
	schemaVersion string,
	mfs *memfs.FS,
	pkgInfo *PackageInfo,
	pkg *packages.Package,
	fset *token.FileSet,
	pass int,
	depNames []string,
) error {
	// When generating a standalone client, dep files live inside the client
	// sub-module directory.  We need to resolve imports relative to that
	// directory so that goimports can find packages like
	// dagger.io/dagger/querybuilder that are declared in the client's go.mod
	// (not in the parent module's go.mod).
	importResolutionDir := cfg.OutputDir
	if cfg.ClientConfig != nil && cfg.ClientConfig.ClientDir != "" {
		importResolutionDir = filepath.Join(cfg.OutputDir, cfg.ClientConfig.ClientDir)
	}

	for _, depName := range depNames {
		depSchema := schema.Include(depName)

		funcs := templates.GoTemplateFuncs(ctx, depSchema, schema, schemaVersion, cfg, pkg, fset, pass)
		tmpl, err := templates.DepTemplate(funcs)
		if err != nil {
			return fmt.Errorf("get dependency template: %w", err)
		}

		dt, err := renderFile(importResolutionDir, depSchema, schemaVersion, pkgInfo, tmpl)
		if err != nil {
			return fmt.Errorf("render dependency file for %q: %w", depName, err)
		}
		if dt == nil {
			// no types for this dependency, skip
			continue
		}

		// Convert dep name to kebab-case for the filename, e.g. "myDep" -> "my-dep.gen.go"
		depFileName := strcase.ToKebab(depName) + ".gen.go"
		depFilePath := filepath.Join(internalDaggerDir, depFileName)

		// Special case for client generation, we want to write the file in the specified client directory.
		if cfg.ClientConfig != nil && cfg.ClientConfig.ClientDir != "" {
			depFilePath = filepath.Join(cfg.ClientConfig.ClientDir, depFileName)
		}

		if err := mfs.WriteFile(depFilePath, dt, 0600); err != nil {
			return fmt.Errorf("write dependency file %q: %w", depFilePath, err)
		}
	}

	return nil
}

func renderFile(
	outputDir string,
	schema *introspection.Schema,
	schemaVersion string,
	pkgInfo *PackageInfo,
	tmpl *template.Template,
) ([]byte, error) {
	data := struct {
		*PackageInfo
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
	}{
		PackageInfo:   pkgInfo,
		Schema:        schema,
		SchemaVersion: schemaVersion,
		Types:         schema.Visit(),
	}

	var render bytes.Buffer
	if err := tmpl.Execute(&render, data); err != nil {
		return nil, err
	}

	source := render.Bytes()
	source = bytes.TrimSpace(source)
	if len(source) == 0 {
		return nil, nil
	}

	formatted, err := format.Source(source)
	if err != nil {
		os.Stderr.Write(source)
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	formatted, err = imports.Process(filepath.Join(outputDir, "dummy.go"), formatted, nil)
	if err != nil {
		os.Stderr.Write(source)
		return nil, fmt.Errorf("error processing imports in generated code: %w", err)
	}
	return formatted, nil
}
