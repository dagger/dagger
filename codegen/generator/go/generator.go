package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/go/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/imports"
)

type GoGenerator struct {
	Config generator.Config
}

func (g *GoGenerator) Generate(ctx context.Context, schema *introspection.Schema) (*generator.GeneratedCode, error) {
	generator.SetSchema(schema)

	funcs := templates.GoTemplateFuncs(g.Config.ModuleName, g.Config.SourceDirectoryPath, schema)

	headerData := struct {
		Package string
		Schema  *introspection.Schema
	}{
		Package: g.Config.Package,
		Schema:  schema,
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

	generatedCode := &generator.GeneratedCode{
		APIClientSource: formatted,
	}
	if g.Config.ModuleName != "" {
		generatedCode.StarterTemplateSource = []byte(g.baseModuleSource())
	}
	return generatedCode, nil
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
