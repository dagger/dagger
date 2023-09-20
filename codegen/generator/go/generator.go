package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/go/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"github.com/vito/progrock"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const ClientGenFile = "dagger.gen.go"
const StarterTemplateFile = "main.go"

type GoGenerator struct {
	Config generator.Config
}

func (g *GoGenerator) Generate(ctx context.Context, schema *introspection.Schema) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	mfs := memfs.New()

	mainMod, needSync, err := g.bootstrapMain(ctx, mfs)
	if err != nil {
		return nil, fmt.Errorf("bootstrap main: %w", err)
	}

	funcs := templates.GoTemplateFuncs(ctx, g.Config.ModuleName, g.Config.SourceDirectoryPath, schema)

	headerData := struct {
		Package  string
		GoModule string
		Schema   *introspection.Schema
	}{
		Package:  g.Config.Package,
		GoModule: mainMod,
		Schema:   schema,
	}

	var render []string

	var header bytes.Buffer
	if err := templates.Header(funcs).Execute(&header, headerData); err != nil {
		return nil, err
	}
	render = append(render, header.String())

	err = schema.Visit(introspection.VisitHandlers{
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

	source := strings.Join(render, "\n")
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w\nsource:\n%s", err, source)
	}
	formatted, err = imports.Process(filepath.Join(g.Config.SourceDirectoryPath, "dummy.go"), formatted, nil)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w\nsource:\n%s", err, source)
	}

	if err := mfs.WriteFile(ClientGenFile, formatted, 0600); err != nil {
		return nil, err
	}

	if err := generator.InstallGitAttributes(mfs, ClientGenFile, g.Config.SourceDirectoryPath); err != nil {
		return nil, err
	}

	// run 'go mod tidy' after generating to fix and prune dependencies
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = g.Config.SourceDirectoryPath

	return &generator.GeneratedState{
		Overlay:        layerfs.New(mfs, dagger.QueryBuilder),
		PostCommands:   []*exec.Cmd{tidyCmd},
		NeedRegenerate: needSync,
	}, nil
}

func (g *GoGenerator) bootstrapMain(ctx context.Context, mfs *memfs.FS) (string, bool, error) {
	srcDir := g.Config.SourceDirectoryPath

	pkgs, err := loadPackages(ctx, srcDir)
	if err != nil {
		return "", false, fmt.Errorf("error loading packages: %w", err)
	}
	var mainPkg *packages.Package
	for _, pkg := range pkgs {
		if pkg.Name == "main" {
			mainPkg = pkg
			break
		}
	}

	if mainPkg != nil {
		progrock.FromContext(ctx).Warn("already bootstrapped")
		// already bootstrapped
		return mainPkg.Module.Path, false, nil
	}

	// write an initial main.go if no main pkg exists yet
	//
	// NB: this has to happen before we run codegen, since it's an input to it.
	if err := mfs.WriteFile(StarterTemplateFile, []byte(g.baseModuleSource()), 0600); err != nil {
		return "", false, err
	}

	// re-try loading main package so that we can detect outer module
	pkgs, err = loadPackages(ctx, g.Config.SourceDirectoryPath)
	if err != nil {
		return "", false, fmt.Errorf("error loading packages: %w", err)
	}
	for _, pkg := range pkgs {
		if pkg.Name == "main" {
			mainPkg = pkg
			break
		}
	}

	// bootstrap go.mod using dependencies from the embedded Go SDK
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return "", false, fmt.Errorf("parse embedded go.mod: %w", err)
	}

	newMod := new(modfile.File)

	var currentMod *modfile.File
	if content, err := os.ReadFile(filepath.Join(srcDir, "go.mod")); err == nil {
		currentMod, err = modfile.Parse("go.mod", content, nil)
		if err != nil {
			return "", false, fmt.Errorf("parse go.mod: %w", err)
		}

		newMod = currentMod

		for _, req := range sdkMod.Require {
			newMod.AddRequire(req.Mod.Path, req.Mod.Version)
		}
	} else {
		var newModName string
		if mainPkg == nil {
			rec := progrock.FromContext(ctx)
			rec.Warn("no main package or module found; falling back to bare module name")
			// fallback to bare module name
			newModName = g.Config.ModuleName
		} else {
			relPath, err := filepath.Rel(mainPkg.Module.Dir, srcDir)
			if err != nil {
				return "", false, fmt.Errorf("error getting relative path: %w", err)
			}

			// base Go module name on outer module path
			//
			// that is, if a repo's root level go.mod is github.com/dagger/dagger, and
			// we're codegenning in ./zenith/foo, the resulting module will be
			// github.com/dagger/dagger/zenith/foo.
			newModName = path.Join(mainPkg.Module.Path, filepath.ToSlash(relPath))
			progrock.FromContext(ctx).Warn("new mod name", progrock.Labelf("mod", newModName))
		}

		newMod.AddModuleStmt(newModName)
		newMod.SetRequire(sdkMod.Require)
	}

	modBody, err := newMod.Format()
	if err != nil {
		return "", false, fmt.Errorf("format go.mod: %w", err)
	}
	if err := mfs.WriteFile("go.mod", modBody, 0600); err != nil {
		return "", false, err
	}
	if err := mfs.WriteFile("go.sum", dagger.GoSum, 0600); err != nil {
		return "", false, err
	}

	return newMod.Module.Mod.Path, true, nil
}

func loadPackages(ctx context.Context, dir string) ([]*packages.Package, error) {
	fset := token.NewFileSet()
	return packages.Load(&packages.Config{
		Context: ctx,
		Dir:     dir,
		Tests:   false,
		Fset:    fset,
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedModule,
	}, ".")
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
