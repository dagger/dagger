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

	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
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
		baseCfg.ModuleConfig = nil
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
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	// respect existing package name
	pkgInfo.PackageName = pkg.Name

	if err := generateCode(ctx, g.Config, schema, mfs, pkgInfo, pkg, fset); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
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
		if g.Config.ModuleConfig != nil {
			outDir, err := filepath.Abs(outDir)
			if err != nil {
				return nil, false, fmt.Errorf("get absolute path: %w", err)
			}
			rootDir, subdirRelPath, err := g.Config.ModuleConfig.RootAndSubpath(outDir)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get module root: %w", err)
			}

			// when a module is configured, look for a go.mod in its root dir instead
			//
			// this is a necessary part of bootstrapping: SDKs such as the Go SDK
			// will want to have a runtime module that lives in the same Go module as
			// the generated client, which typically lives in the parent directory.
			if pkg, _, err := loadPackage(ctx, rootDir); err == nil {
				return &PackageInfo{
					// leave package name blank
					// TODO: maybe we don't even need to return it?
					PackageImport: path.Join(pkg.Module.Path, subdirRelPath),
				}, false, nil
			}

			// bootstrap go.mod using dependencies from the embedded Go SDK

			newModName := "main" // use a safe default, no going to be a reserved word. User is free to modify

			newMod.AddModuleStmt(newModName)
			newMod.SetRequire(sdkMod.Require)

			info.PackageImport = newModName

			needsRegen = true
		} else {
			// no module; assume client-only codegen

			if pkg, _, err := loadPackage(ctx, outDir); err == nil {
				return &PackageInfo{
					PackageName:   pkg.Name,
					PackageImport: pkg.Module.Path,
				}, false, nil
			}

			return nil, false, fmt.Errorf("no module name configured and no existing package found")
		}
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
	funcs := templates.GoTemplateFuncs(ctx, schema, cfg.ModuleConfig, pkg, fset)

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
				IsModuleCode: cfg.ModuleConfig != nil,
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

	if cfg.ModuleConfig != nil {
		moduleData := struct {
			Schema *introspection.Schema
		}{
			Schema: schema,
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
	formatted, err = imports.Process(filepath.Join(cfg.OutputDir, "dummy.go"), formatted, nil)
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

	f, _ := os.Open(path.Join(dir, ".git/index"))
	if f != nil {
		fmt.Println(digest.FromReader(f))
	}

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

	f, _ = os.Open(path.Join(dir, ".git/index"))
	if f != nil {
		fmt.Println(digest.FromReader(f))
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
	moduleStructName := strcase.ToCamel(g.Config.ModuleConfig.Name)

	return fmt.Sprintf(`package main

import (
	"context"
)

type %s struct {}

// example usage: "dagger call container-echo --string-arg yo"
func (m *%s) ContainerEcho(stringArg string) *Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

// example usage: "dagger call grep-dir --directory-arg . --pattern GrepDir"
func (m *%s) GrepDir(ctx context.Context, directoryArg *Directory, pattern string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", directoryArg).
		WithWorkdir("/mnt").
		WithExec([]string{"grep", "-R", pattern, "."}).
		Stdout(ctx)
}

`, moduleStructName, moduleStructName, moduleStructName)
}
