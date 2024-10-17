package gogenerator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/trace"
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

func (g *GoGenerator) Generate(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	// 1. if no go.mod, generate go.mod
	// 2. if no .go files, bootstrap package main
	//  2a. generate dagger.gen.go from base client,
	//  2b. add stub main.go
	// 3. load package, generate dagger.gen.go (possibly again)

	outDir := "."
	if g.Config.ModuleName != "" {
		outDir = filepath.Clean(g.Config.ModuleContextPath)
	}

	mfs := memfs.New()

	var overlay fs.FS = mfs
	if g.Config.ModuleName != "" {
		overlay = layerfs.New(
			mfs,
			&MountedFS{FS: dagger.QueryBuilder, Name: filepath.Join(outDir, "internal")},
			&MountedFS{FS: dagger.Telemetry, Name: filepath.Join(outDir, "internal")},
		)
	}

	genSt := &generator.GeneratedState{
		Overlay: overlay,
		PostCommands: []*exec.Cmd{
			// run 'go mod tidy' after generating to fix and prune dependencies
			exec.Command("go", "mod", "tidy"),
		},
	}
	if _, err := os.Stat(filepath.Join(g.Config.OutputDir, "go.work")); err == nil {
		// run "go work use ." after generating if we had a go.work at the root
		genSt.PostCommands = append(genSt.PostCommands, exec.Command("go", "work", "use", "."))
	}

	pkgInfo, partial, err := g.bootstrapMod(ctx, mfs)
	if err != nil {
		return nil, fmt.Errorf("bootstrap package: %w", err)
	}
	if outDir != "." {
		mfs.MkdirAll(outDir, 0700)
		fs, err := mfs.Sub(outDir)
		if err != nil {
			return nil, err
		}
		mfs = fs.(*memfs.FS)
	}

	initialGoFiles, err := filepath.Glob(filepath.Join(g.Config.OutputDir, outDir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("glob go files: %w", err)
	}

	genFile := filepath.Join(g.Config.OutputDir, outDir, ClientGenFile)
	if _, err := os.Stat(genFile); err != nil {
		// assume package main, default for modules
		pkgInfo.PackageName = "main"

		// generate an initial dagger.gen.go from the base Dagger API
		if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, nil, nil, 0); err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}

		partial = true
	}

	if len(initialGoFiles) == 0 {
		// write an initial main.go if no main pkg exists yet
		if err := mfs.WriteFile(StarterTemplateFile, []byte(g.baseModuleSource(pkgInfo)), 0600); err != nil {
			return nil, err
		}

		// main.go is actually an input to codegen, so this requires another pass
		partial = true
	}

	if partial {
		genSt.NeedRegenerate = true
		return genSt, nil
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir))
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	// respect existing package name
	pkgInfo.PackageName = pkg.Name

	if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, pkg, fset, 1); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
}

type PackageInfo struct {
	PackageName   string // Go package name, typically "main"
	PackageImport string // import path of package in which this file appears
}

func (g *GoGenerator) bootstrapMod(ctx context.Context, mfs *memfs.FS) (*PackageInfo, bool, error) {
	// don't mess around go.mod if we're not building modules
	if g.Config.ModuleName == "" {
		if pkg, _, err := loadPackage(ctx, g.Config.OutputDir); err == nil {
			return &PackageInfo{
				PackageName:   pkg.Name,
				PackageImport: pkg.Module.Path,
			}, false, nil
		}
		return nil, false, fmt.Errorf("no module name configured and no existing package found")
	}

	var needsRegen bool

	var modPath string
	var mod *modfile.File

	modname := fmt.Sprintf("dagger/%s", strcase.ToKebab(g.Config.ModuleName))
	// check for a go.mod already for the dagger module
	if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, g.Config.ModuleContextPath, "go.mod")); err == nil {
		modPath = g.Config.ModuleContextPath

		mod, err = modfile.ParseLax("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		if g.Config.Merge != nil && !*g.Config.Merge && mod.Module.Mod.Path != modname {
			return nil, false, fmt.Errorf("existing go.mod does not match the module's path")
		}
	}

	// if no go.mod is available, check the root output directory instead
	// and if no merge is set
	//
	// this is a necessary part of bootstrapping: SDKs such as the Go SDK
	// will want to have a runtime module that lives in the same Go module as
	// the generated client, which typically lives in the parent directory.
	if mod == nil && (g.Config.Merge == nil || *g.Config.Merge) {
		if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, "go.mod")); err == nil {
			modPath = "."
			mod, err = modfile.ParseLax("go.mod", content, nil)
			if err != nil {
				return nil, false, fmt.Errorf("parse go.mod: %w", err)
			}
		}
	}
	// could not find a go.mod, so we can init a basic one
	if mod == nil {
		modPath = g.Config.ModuleContextPath
		mod = new(modfile.File)

		mod.AddModuleStmt(modname)
		mod.AddGoStmt(goVersion)

		needsRegen = true
	}

	// sanity check the parsed go version
	//
	// if this fails, then the go.mod version is too high! and in that case, we
	// won't be able to load the resulting package
	if semver.Compare("v"+mod.Go.Version, "v"+goVersion) > 0 {
		return nil, false, fmt.Errorf("existing go.mod has unsupported version %v (highest supported version is %v)", mod.Go.Version, goVersion)
	}

	// use Go SDK's embedded go.mod as basis for pinning versions
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return nil, false, fmt.Errorf("parse embedded go.mod: %w", err)
	}
	modRequires := make(map[string]*modfile.Require)
	for _, req := range mod.Require {
		modRequires[req.Mod.Path] = req
	}
	for _, minReq := range sdkMod.Require {
		// check if mod already at least this version
		if currentReq, ok := modRequires[minReq.Mod.Path]; ok {
			if semver.Compare(currentReq.Mod.Version, minReq.Mod.Version) >= 0 {
				continue
			}
		}
		modRequires[minReq.Mod.Path] = minReq
		mod.AddNewRequire(minReq.Mod.Path, minReq.Mod.Version, minReq.Indirect)
	}
	// preserve any replace directives in sdk/go's go.mod (e.g. pre-1.0 packages)
	for _, minReq := range sdkMod.Replace {
		if _, ok := modRequires[minReq.New.Path]; ok { // ignore anything that's sdk/go only
			mod.AddReplace(minReq.Old.Path, minReq.Old.Version, minReq.New.Path, minReq.New.Version)
		}
	}

	// try and find a go.sum next to the go.mod, and use that to pin
	sum, err := os.ReadFile(filepath.Join(g.Config.OutputDir, modPath, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("could not read go.sum: %w", err)
	}
	sum = append(sum, '\n')
	sum = append(sum, dagger.GoSum...)

	modBody, err := mod.Format()
	if err != nil {
		return nil, false, fmt.Errorf("format go.mod: %w", err)
	}

	if err := mfs.MkdirAll(modPath, 0700); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(modPath, "go.mod"), modBody, 0600); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(modPath, "go.sum"), sum, 0600); err != nil {
		return nil, false, err
	}

	packageImport, err := filepath.Rel(modPath, g.Config.ModuleContextPath)
	if err != nil {
		return nil, false, err
	}
	return &PackageInfo{
		// PackageName is unknown until we load the package
		PackageImport: path.Join(mod.Module.Mod.Path, packageImport),
	}, needsRegen, nil
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
	funcs := templates.GoTemplateFuncs(ctx, schema, schemaVersion, cfg.ModuleName, cfg.ModuleParentPath, pkg, fset, pass)
	tmpls := templates.Templates(funcs)

	for k, tmpl := range tmpls {
		dt, err := renderFile(cfg, schema, schemaVersion, pkgInfo, tmpl)
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
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	formatted, err = imports.Process(filepath.Join(cfg.OutputDir, "dummy.go"), formatted, nil)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	return formatted, nil
}

func loadPackage(ctx context.Context, dir string) (*packages.Package, *token.FileSet, error) {
	ctx, span := trace.Tracer().Start(ctx, "loadPackage")
	defer span.End()

	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Context: ctx,
		Dir:     dir,
		Tests:   false,
		Fset:    fset,
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedModule,
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			astFile, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			// strip function bodies since we don't need them and don't need to waste time in packages.Load with type checking them
			for _, decl := range astFile.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					fn.Body = nil
				}
			}
			return astFile, nil
		},
		// Print some debug logs with timing information to stdout
		Logf: func(format string, args ...interface{}) {
			fmt.Printf(format+"\n", args...)
		},
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

func (g *GoGenerator) baseModuleSource(pkgInfo *PackageInfo) string {
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
	"%[2]s/internal/dagger"
)

type %[1]s struct{}

// Returns a container that echoes whatever string argument is provided
func (m *%[1]s) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

// Returns lines that match a pattern in the files of the provided Directory
func (m *%[1]s) GrepDir(ctx context.Context, directoryArg *dagger.Directory, pattern string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", directoryArg).
		WithWorkdir("/mnt").
		WithExec([]string{"grep", "-R", pattern, "."}).
		Stdout(ctx)
}
`, moduleStructName, pkgInfo.PackageImport)
}
