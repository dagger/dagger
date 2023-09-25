package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"os/exec"
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

	pkgInfo, needsRegen, err := g.bootstrapPkg(ctx, mfs)
	if err != nil {
		return nil, fmt.Errorf("bootstrap package: %w", err)
	}

	if !needsRegen {
		err := g.generateCode(ctx, schema, mfs, pkgInfo)
		if err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}
	}

	return &generator.GeneratedState{
		Overlay: layerfs.New(mfs, dagger.QueryBuilder),
		PostCommands: []*exec.Cmd{
			// run 'go mod tidy' after generating to fix and prune dependencies
			exec.Command("go", "mod", "tidy"),
		},
		NeedRegenerate: needsRegen,
	}, nil
}

type PackageInfo struct {
	PackageName string // Go package name, typically "main"
	ModulePath  string // import path of package in which this file appears
}

func (g *GoGenerator) bootstrapPkg(ctx context.Context, mfs *memfs.FS) (*PackageInfo, bool, error) {
	rec := progrock.FromContext(ctx)

	var needsRegen bool

	outDir := g.Config.OutputDir

	info := &PackageInfo{}

	// bootstrap go.mod using dependencies from the embedded Go SDK
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return nil, false, fmt.Errorf("parse embedded go.mod: %w", err)
	}

	newMod := new(modfile.File)

	// respect existing go.mod
	if content, err := os.ReadFile(filepath.Join(outDir, "go.mod")); err == nil {
		currentMod, err := modfile.Parse("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		newMod = currentMod

		for _, req := range sdkMod.Require {
			newMod.AddRequire(req.Mod.Path, req.Mod.Version)
		}

		info.ModulePath = currentMod.Module.Mod.Path
	} else {
		newModName := strcase.ToKebab(g.Config.ModuleName)

		newMod.AddModuleStmt(newModName)
		newMod.SetRequire(sdkMod.Require)

		info.ModulePath = newModName

		needsRegen = true
	}

	modBody, err := newMod.Format()
	if err != nil {
		return nil, false, fmt.Errorf("format go.mod: %w", err)
	}
	if err := mfs.WriteFile("go.mod", modBody, 0600); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile("go.sum", dagger.GoSum, 0600); err != nil {
		return nil, false, err
	}

	if needsRegen {
		// need go.mod to exist for us to be able to loadPackage below, so just
		// wait for the next regen pass
		return info, true, nil
	}

	if modPkg, _, loadPkgErr := loadPackage(ctx, outDir); loadPkgErr == nil {
		msgOpts := []progrock.MessageOpt{
			progrock.Labelf("pkgName", modPkg.Name),
			progrock.Labelf("pkgPath", modPkg.PkgPath),
		}
		if modPkg.Module != nil {
			msgOpts = append(msgOpts, progrock.Labelf("module", modPkg.Module.Path))
		}
		rec.Debug("found existing Go package", msgOpts...)
		info.PackageName = modPkg.Name
	} else {
		rec.Debug("bootstrapping main package", progrock.ErrorLabel(loadPkgErr))

		info.PackageName = "main"

		if matches, err := filepath.Glob(filepath.Join(outDir, "*.go")); err == nil && len(matches) == 0 {
			// write an initial main.go if no main pkg exists yet
			if err := mfs.WriteFile(StarterTemplateFile, []byte(g.baseModuleSource()), 0600); err != nil {
				return nil, false, err
			}

			// we just generated code that is actually an input to codegen, so this
			// will take two passes
			needsRegen = true
		}
	}

	return info, needsRegen, nil
}

func (g *GoGenerator) generateCode(ctx context.Context, schema *introspection.Schema, mfs *memfs.FS, pkgInfo *PackageInfo) error {
	pkg, fset, err := loadPackage(ctx, g.Config.SourceDir)
	if err != nil {
		return fmt.Errorf("load package: %w", err)
	}

	funcs := templates.GoTemplateFuncs(ctx, schema, g.Config.ModuleName, pkg, fset)

	headerData := struct {
		*PackageInfo
		Schema *introspection.Schema
	}{
		PackageInfo: pkgInfo,
		Schema:      schema,
	}

	var render []string

	var header bytes.Buffer
	if err := templates.Header(funcs).Execute(&header, headerData); err != nil {
		return err
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
		return err
	}

	if g.Config.ModuleName != "" {
		moduleData := struct {
			Schema              *introspection.Schema
			SourceDirectoryPath string
		}{
			Schema:              schema,
			SourceDirectoryPath: g.Config.SourceDir,
		}

		var moduleMain bytes.Buffer
		if err := templates.Module(funcs).Execute(&moduleMain, moduleData); err != nil {
			return err
		}
		render = append(render, moduleMain.String())
	}

	source := strings.Join(render, "\n")
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return fmt.Errorf("error formatting generated code: %T %+v %w\nsource:\n%s", err, err, err, source)
	}
	formatted, err = imports.Process(filepath.Join(g.Config.SourceDir, "dummy.go"), formatted, nil)
	if err != nil {
		return fmt.Errorf("error formatting generated code: %T %+v %w\nsource:\n%s", err, err, err, source)
	}

	if err := mfs.WriteFile(ClientGenFile, formatted, 0600); err != nil {
		return err
	}

	if err := generator.InstallGitAttributes(mfs, ClientGenFile, g.Config.OutputDir); err != nil {
		return err
	}

	return nil
}

func loadPackage(ctx context.Context, dir string) (*packages.Package, *token.FileSet, error) {
	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
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
	if err != nil {
		return nil, nil, err
	}
	switch len(pkgs) {
	case 0:
		return nil, nil, fmt.Errorf("no packages found in %s", dir)
	case 1:
		if pkgs[0].Name == "" {
			// this happens when loading an empty dir within an existing Go module
			return nil, nil, fmt.Errorf("package name is empty")
		}
		return pkgs[0], fset, nil
	default:
		// this would mean I don't understand how loading '.' works
		return nil, nil, fmt.Errorf("expected 1 package, got %d", len(pkgs))
	}
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
