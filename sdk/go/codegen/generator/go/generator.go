package gogenerator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen/generator"
	"dagger.io/dagger/codegen/generator/go/templates"
	"dagger.io/dagger/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/go-git/go-git/v5"
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

	// 1. if no go.mod, generate go.mod
	// 2. if no .go files, bootstrap package main
	//  2a. generate dagger.gen.go from base client,
	//  2b. add stub main.go
	// 3. load package, generate dagger.gen.go (possibly again)

	mfs := memfs.New()

	genSt := &generator.GeneratedState{
		Overlay: layerfs.New(mfs, dagger.QueryBuilder),
		PostCommands: []*exec.Cmd{
			// run 'go mod tidy' after generating to fix and prune dependencies
			exec.Command("go", "mod", "tidy"),
		},
	}

	pkgInfo, partial, err := g.bootstrapMod(ctx, mfs)
	if err != nil {
		return nil, fmt.Errorf("bootstrap package: %w", err)
	}

	outDir := g.Config.OutputDir

	initialGoFiles, err := filepath.Glob(filepath.Join(outDir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("glob go files: %w", err)
	}

	genFile := filepath.Join(outDir, ClientGenFile)
	if _, err := os.Stat(genFile); err != nil {
		// assume package main, default for modules
		pkgInfo.PackageName = "main"

		// generate an initial dagger.gen.go from the base Dagger API
		baseCfg := g.Config
		baseCfg.ModuleName = ""
		baseCfg.ModuleSourceDir = ""
		if err := generateCode(ctx, baseCfg, schema, mfs, pkgInfo, nil, nil); err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}

		partial = true
	}

	if len(initialGoFiles) == 0 {
		// write an initial main.go if no main pkg exists yet
		if err := mfs.WriteFile(StarterTemplateFile, []byte(g.baseModuleSource()), 0600); err != nil {
			return nil, err
		}

		// main.go is actually an input to codegen, so this requires another pass
		partial = true
	}

	if partial {
		genSt.NeedRegenerate = true
		return genSt, nil
	}

	pkg, fset, err := loadPackage(ctx, outDir)
	if err != nil {
		return nil, fmt.Errorf("load package: %w", err)
	}

	// respect existing package name
	pkgInfo.PackageName = pkg.Name

	// automate VCS first so we re-add any files cleaned up from the transition
	// to using .gitignore for generated files
	if err := g.automateVCS(ctx, mfs); err != nil {
		return nil, fmt.Errorf("automate vcs: %w", err)
	}

	if err := generateCode(ctx, g.Config, schema, mfs, pkgInfo, pkg, fset); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
}

func (g *GoGenerator) automateVCS(ctx context.Context, mfs *memfs.FS) error {
	rec := progrock.FromContext(ctx)

	if !g.Config.AutomateVCS {
		rec.Debug("skipping VCS automation (disabled)")
		// TODO disable this for on-the-fly codegen
		return nil
	}

	repo, err := git.PlainOpenWithOptions(g.Config.OutputDir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			// not in a repo
			rec.Debug("repo not found; skipping VCS automation")
			return nil
		}
		return fmt.Errorf("open git repo: %w", err)
	}

	rec.Debug("updating .gitattributes")

	if err := generator.MarkGeneratedAttributes(mfs, g.Config.OutputDir, ClientGenFile); err != nil {
		return fmt.Errorf("update .gitattributes: %w", err)
	}

	rec.Debug("updating .gitignore")

	if err := generator.GitIgnorePaths(ctx, repo, mfs, g.Config.OutputDir,
		ClientGenFile,
		"internal/",
		"querybuilder/", // now lives under internal/
	); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	return nil
}

type PackageInfo struct {
	PackageName   string // Go package name, typically "main"
	PackageImport string // import path of package in which this file appears
}

func (g *GoGenerator) bootstrapMod(ctx context.Context, mfs *memfs.FS) (*PackageInfo, bool, error) {
	var needsRegen bool

	outDir := g.Config.OutputDir

	info := &PackageInfo{}

	// use embedded go.mod as basis for pinning versions
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return nil, false, fmt.Errorf("parse embedded go.mod: %w", err)
	}

	newMod := new(modfile.File)

	if content, err := os.ReadFile(filepath.Join(outDir, "go.mod")); err == nil {
		// respect existing go.mod

		currentMod, err := modfile.Parse("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		newMod = currentMod

		for _, req := range sdkMod.Require {
			newMod.AddRequire(req.Mod.Path, req.Mod.Version)
		}

		info.PackageImport = currentMod.Module.Mod.Path
	} else {
		if g.Config.ModuleRootDir != "" {
			// when a root dir is configured, look for a go.mod there instead
			//
			// this is a necessary part of bootstrapping: SDKs such as the Go SDK
			// will want to have a runtime module that lives in the same Go module as
			// the generated client, which typically lives in the parent directory.
			if pkg, _, err := loadPackage(ctx, g.Config.ModuleRootDir); err == nil {
				modSrcDir, err := filepath.Abs(g.Config.ModuleSourceDir)
				if err != nil {
					return nil, false, fmt.Errorf("failed to get module root: %w", err)
				}
				modRootDir, err := filepath.Abs(filepath.Join(g.Config.ModuleSourceDir, g.Config.ModuleRootDir))
				if err != nil {
					return nil, false, fmt.Errorf("failed to get module root: %w", err)
				}
				subdirRelPath, err := filepath.Rel(modRootDir, modSrcDir)
				if err != nil {
					return nil, false, fmt.Errorf("failed to get subdir relative path: %w", err)
				}
				return &PackageInfo{
					// leave package name blank
					// TODO: maybe we don't even need to return it?
					PackageImport: path.Join(pkg.Module.Path, subdirRelPath),
				}, false, nil
			}
		}

		// bootstrap go.mod using dependencies from the embedded Go SDK

		newModName := strcase.ToKebab(g.Config.ModuleName)

		newMod.AddModuleStmt(newModName)
		newMod.SetRequire(sdkMod.Require)

		info.PackageImport = newModName

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

	return info, needsRegen, nil
}

func generateCode(
	ctx context.Context,
	cfg generator.Config,
	schema *introspection.Schema,
	mfs *memfs.FS,
	pkgInfo *PackageInfo,
	pkg *packages.Package,
	fset *token.FileSet,
) error {
	funcs := templates.GoTemplateFuncs(ctx, schema, cfg.ModuleName, pkg, fset)

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
				IsModuleCode: cfg.ModuleName != "",
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

	if cfg.ModuleName != "" {
		moduleData := struct {
			Schema              *introspection.Schema
			SourceDirectoryPath string
		}{
			Schema:              schema,
			SourceDirectoryPath: cfg.ModuleSourceDir,
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
	formatted, err = imports.Process(filepath.Join(cfg.ModuleSourceDir, "dummy.go"), formatted, nil)
	if err != nil {
		return fmt.Errorf("error formatting generated code: %T %+v %w\nsource:\n%s", err, err, err, source)
	}

	if err := mfs.WriteFile(ClientGenFile, formatted, 0600); err != nil {
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
