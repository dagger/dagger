package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/blang/semver"
	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"dagger.io/dagger"
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

var goVersion = semver.MustParse(strings.TrimPrefix(runtime.Version(), "go"))

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

	var overlay fs.FS = mfs
	if g.Config.ModuleName != "" {
		overlay = layerfs.New(mfs, &MountedFS{FS: dagger.QueryBuilder, Name: "internal"})
	}

	genSt := &generator.GeneratedState{
		Overlay: overlay,
		PostCommands: []*exec.Cmd{
			// run 'go mod tidy' after generating to fix and prune dependencies
			exec.Command("go", "mod", "tidy"),
		},
	}
	if _, err := os.Stat(filepath.Join(g.Config.ModuleContextPath, "go.work")); err == nil {
		// run "go work use ." after generating if we had a go.work at the root
		genSt.PostCommands = append(genSt.PostCommands, exec.Command("go", "work", "use", "."))
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
		if err := generateCode(ctx, g.Config, schema, mfs, pkgInfo, nil, nil, 0); err != nil {
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

	if err := generateCode(ctx, g.Config, schema, mfs, pkgInfo, pkg, fset, 1); err != nil {
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
		currentModGoVersion, err := semver.Parse(currentMod.Go.Version)
		if err != nil {
			var err2 error
			currentModGoVersion, err2 = semver.Parse(currentMod.Go.Version + ".0")
			if err2 != nil {
				return nil, false, fmt.Errorf("parse go.mod version %q: %w", currentMod.Go.Version, err)
			}
		}
		if currentModGoVersion.GT(goVersion) {
			return nil, false, fmt.Errorf("existing go.mod has unsupported version %v (highest supported version is %v)", currentMod.Go.Version, goVersion)
		}
		newMod = currentMod

		for _, req := range sdkMod.Require {
			newMod.AddRequire(req.Mod.Path, req.Mod.Version)
		}

		info.PackageImport = currentMod.Module.Mod.Path
	} else {
		if g.Config.ModuleName != "" {
			outDir, err := filepath.Abs(outDir)
			if err != nil {
				return nil, false, fmt.Errorf("get absolute path: %w", err)
			}
			rootDir := g.Config.ModuleContextPath
			subdirRelPath, err := filepath.Rel(rootDir, outDir)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get output dir rel path: %w", err)
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

			newModName := fmt.Sprintf("dagger/%s", strcase.ToKebab(g.Config.ModuleName))

			newMod.AddModuleStmt(newModName)
			newMod.AddGoStmt(goVersion.String())
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
	pass int,
) error {
	funcs := templates.GoTemplateFuncs(ctx, schema, cfg.ModuleName, pkg, fset, pass)
	tmpls := templates.Templates(funcs)

	for k, tmpl := range tmpls {
		dt, err := renderFile(cfg, schema, pkgInfo, tmpl)
		if err != nil {
			return err
		}
		if dt == nil {
			// no contents, skip
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
	cfg generator.Config,
	schema *introspection.Schema,
	pkgInfo *PackageInfo,
	tmpl *template.Template,
) ([]byte, error) {
	data := struct {
		*PackageInfo
		Schema *introspection.Schema
		Types  []*introspection.Type
	}{
		PackageInfo: pkgInfo,
		Schema:      schema,
		Types:       schema.Visit(),
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
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	formatted, err = imports.Process(filepath.Join(cfg.OutputDir, "dummy.go"), formatted, nil)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	return formatted, nil
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
			// this can happen when:
			// - loading an empty dir within an existing Go module
			// - loading a dir that is not included in a parent go.work
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

	return fmt.Sprintf(`// A generated module for %[1]s functions
//
// This module has been generated via dagger init and serves as a reference to
// basic module structure as you get started with Dagger.
//
// Two functions have been pre-created. You can modify, delete, or add to them,
// as needed. They demonstrate usage of arguments and return types using simple
// echo and grep commands. The functions can be called from the dagger CLI or
// from one of the SDKs.
//
// The first line in this comment block is a short description line and the
// rest is a long description with more detail on the module's purpose or usage,
// if appropriate. All modules should have a short description.

package main

import (
	"context"
)

type %[1]s struct{}

// Returns a container that echoes whatever string argument is provided
func (m *%[1]s) ContainerEcho(stringArg string) *Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

// Returns lines that match a pattern in the files of the provided Directory
func (m *%[1]s) GrepDir(ctx context.Context, directoryArg *Directory, pattern string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", directoryArg).
		WithWorkdir("/mnt").
		WithExec([]string{"grep", "-R", pattern, "."}).
		Stdout(ctx)
}
`, moduleStructName)
}
