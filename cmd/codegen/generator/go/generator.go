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
	"strings"
	"text/template"

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
)

var goVersion = strings.TrimPrefix(runtime.Version(), "go")

type GoGenerator struct {
	Config generator.Config
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
	funcs := templates.GoTemplateFuncs(ctx, schema, schemaVersion, cfg, pkg, fset, pass)
	tmpls := templates.Templates(funcs)

	for k, tmpl := range tmpls {
		dt, err := renderFile(cfg.OutputDir, schema, schemaVersion, pkgInfo, tmpl)
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
