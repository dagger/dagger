package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/go/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/imports"
)

const ClientGenFile = "dagger.gen.go"
const StarterTemplateFile = "main.go"

type GoGenerator struct {
	Config generator.Config
}

func (g *GoGenerator) Generate(ctx context.Context, schema *introspection.Schema) (fs.FS, error) {
	generator.SetSchema(schema)

	funcs := templates.GoTemplateFuncs(g.Config.ModuleName, g.Config.SourceDirectoryPath, schema)

	headerData := struct {
		Package  string
		GoModule string
		Schema   *introspection.Schema
	}{
		Package: g.Config.Package,
		Schema:  schema,
	}

	currentModPath := filepath.Join(g.Config.SourceDirectoryPath, "go.mod")
	if modContent, err := os.ReadFile(currentModPath); err == nil {
		modFile, err := modfile.Parse(currentModPath, modContent, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", currentModPath, err)
		}
		headerData.GoModule = modFile.Module.Mod.Path
	} else {
		// TODO: detect this based on git repo or outer go.mod
		headerData.GoModule = "dagger.io/" + g.Config.ModuleName
	}

	var render []string

	var header bytes.Buffer
	if err := templates.Header(funcs).Execute(&header, headerData); err != nil {
		return nil, err
	}
	render = append(render, header.String())

	err := schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Scalar(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Object(funcs).Execute(&out, struct {
				*introspection.Type
				IsModuleCode bool
			}{
				Type:         t,
				IsModuleCode: g.Config.ModuleName != "",
			}); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Enum: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Enum(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Input: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Input(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	if g.Config.ModuleName != "" {
		moduleData := struct {
			Schema              *introspection.Schema
			SourceDirectoryPath string
		}{
			Schema:              schema,
			SourceDirectoryPath: g.Config.SourceDirectoryPath,
		}

		var moduleMain bytes.Buffer
		if err := templates.Module(funcs).Execute(&moduleMain, moduleData); err != nil {
			return nil, err
		}
		render = append(render, moduleMain.String())
	}

	formatted, err := format.Source(
		[]byte(strings.Join(render, "\n")),
	)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	formatted, err = imports.Process(filepath.Join(g.Config.SourceDirectoryPath, "dummy.go"), formatted, nil)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}

	mfs := memfs.New()

	if err := mfs.WriteFile(ClientGenFile, formatted, 0600); err != nil {
		return nil, err
	}

	gitAttributes := fmt.Sprintf("/%s linguist-generated=true", ClientGenFile)
	if err := mfs.WriteFile(".gitattributes", []byte(gitAttributes), 0600); err != nil {
		return nil, err
	}

	// only write a main.go if it doesn't exist
	liveTemplatePath := filepath.Join(g.Config.SourceDirectoryPath, StarterTemplateFile)
	if _, err := os.Stat(liveTemplatePath); err != nil && g.Config.ModuleName != "" {
		if err := mfs.WriteFile(StarterTemplateFile, []byte(g.baseModuleSource()), 0600); err != nil {
			return nil, err
		}
	}

	return layerfs.New(mfs, dagger.QueryBuilder), nil
}

func (g *GoGenerator) baseModuleSource() string {
	moduleStructName := strcase.ToCamel(g.Config.ModuleName)
	return fmt.Sprintf(`package main

import (
	"context"
)

type %s struct {}

func (m *%s) MyFunction(ctx context.Context, stringArg string) (*Container, error) {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg}).Sync(ctx)
}
`, moduleStructName, moduleStructName)
}
