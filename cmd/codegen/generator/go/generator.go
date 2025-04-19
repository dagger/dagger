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
	"dagger.io/dagger/telemetry"
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

func (g *GoGenerator) GenerateModule(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	// 1. if no go.mod, generate go.mod
	// 2. if no .go files, bootstrap package main
	//  2a. generate dagger.gen.go from base client,
	//  2b. add stub main.go
	// 3. load package, generate dagger.gen.go (possibly again)

	outDir := "."
	if g.Config.ModuleName != "" {
		outDir = filepath.Clean(g.Config.ModuleSourcePath)
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
	}

	pkgInfo, partial, err := g.bootstrapMod(ctx, mfs, genSt)
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

func (g *GoGenerator) GenerateClient(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	outDir := "."
	mfs := memfs.New()

	layers := []fs.FS{mfs}

	// Use the published package library for external dagger packages.
	packageImport := "dagger.io/dagger"

	// If dev is set, we need to add local files to the overlay and change the package import
	if g.Config.Dev {
		layers = append(
			layers,
			&MountedFS{FS: dagger.QueryBuilder, Name: "internal"},
			&MountedFS{FS: dagger.EngineConn, Name: "internal"},
		)

		// Get the go package from the module
		// We assume that we'll be located at the root source directory
		pkg, _, err := loadPackage(ctx, ".")
		if err != nil {
			return nil, fmt.Errorf("load package %q: %w", outDir, err)
		}

		// respect existing package import path
		packageImport = filepath.Join(pkg.Module.Path, g.Config.OutputDir)
	}

	genSt := &generator.GeneratedState{
		Overlay: layerfs.New(layers...),
		PostCommands: []*exec.Cmd{
			exec.Command("go", "mod", "tidy"),
		},
	}

	packageName := "dagger"
	if g.Config.OutputDir == "." {
		packageName = "main"
	}

	if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, &PackageInfo{
		PackageName: packageName,

		PackageImport: packageImport,
	}, nil, nil, 1); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
}

type PackageInfo struct {
	PackageName   string // Go package name, typically "main"
	PackageImport string // import path of package in which this file appears
}

func (g *GoGenerator) bootstrapMod(ctx context.Context, mfs *memfs.FS, genSt *generator.GeneratedState) (*PackageInfo, bool, error) {
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

	var daggerModPath string
	var goMod *modfile.File

	modname := fmt.Sprintf("dagger/%s", strcase.ToKebab(g.Config.ModuleName))
	// check for a go.mod already for the dagger module
	if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, g.Config.ModuleSourcePath, "go.mod")); err == nil {
		daggerModPath = g.Config.ModuleSourcePath

		goMod, err = modfile.ParseLax("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		if g.Config.IsInit && goMod.Module.Mod.Path != modname {
			return nil, false, fmt.Errorf("existing go.mod path %q does not match the module's name %q", goMod.Module.Mod.Path, modname)
		}
	}

	// if no go.mod is available and we are merging with the projects parent when possible,
	// check the root output directory instead
	//
	// this is a necessary part of bootstrapping: SDKs such as the Go SDK
	// will want to have a runtime module that lives in the same Go module as
	// the generated client, which typically lives in the parent directory.
	if goMod == nil && g.Config.Merge {
		if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, "go.mod")); err == nil {
			daggerModPath = "."
			goMod, err = modfile.ParseLax("go.mod", content, nil)
			if err != nil {
				return nil, false, fmt.Errorf("parse go.mod: %w", err)
			}
		}
	}
	// could not find a go.mod, so we can init a basic one
	if goMod == nil {
		daggerModPath = g.Config.ModuleSourcePath
		goMod = new(modfile.File)

		goMod.AddModuleStmt(modname)
		goMod.AddGoStmt(goVersion)

		needsRegen = true
	}

	// sanity check the parsed go version
	//
	// if this fails, then the go.mod version is too high! and in that case, we
	// won't be able to load the resulting package
	if goMod.Go == nil {
		return nil, false, fmt.Errorf("go.mod has no go directive")
	}
	if semver.Compare("v"+goMod.Go.Version, "v"+goVersion) > 0 {
		return nil, false, fmt.Errorf("existing go.mod has unsupported version %v (highest supported version is %v)", goMod.Go.Version, goVersion)
	}

	if err := g.syncModReplaceAndTidy(goMod, genSt, daggerModPath); err != nil {
		return nil, false, err
	}

	// try and find a go.sum next to the go.mod, and use that to pin
	sum, err := os.ReadFile(filepath.Join(g.Config.OutputDir, daggerModPath, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("could not read go.sum: %w", err)
	}
	sum = append(sum, '\n')
	sum = append(sum, dagger.GoSum...)

	modBody, err := goMod.Format()
	if err != nil {
		return nil, false, fmt.Errorf("format go.mod: %w", err)
	}

	if err := mfs.MkdirAll(daggerModPath, 0700); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(daggerModPath, "go.mod"), modBody, 0600); err != nil {
		return nil, false, err
	}
	if err := mfs.WriteFile(filepath.Join(daggerModPath, "go.sum"), sum, 0600); err != nil {
		return nil, false, err
	}

	packageImport, err := filepath.Rel(daggerModPath, g.Config.ModuleSourcePath)
	if err != nil {
		return nil, false, err
	}
	return &PackageInfo{
		// PackageName is unknown until we load the package
		PackageImport: path.Join(goMod.Module.Mod.Path, packageImport),
	}, needsRegen, nil
}

func (g *GoGenerator) syncModReplaceAndTidy(mod *modfile.File, genSt *generator.GeneratedState, modPath string) error {
	modDir := filepath.Join(g.Config.OutputDir, modPath)

	// if there is a go.work, we need to also set overrides there, otherwise
	// modules will have individually conflicting replace directives
	goWork, err := goEnv(modDir, "GOWORK")
	if err != nil {
		return fmt.Errorf("find go.work: %w", err)
	}

	// use Go SDK's embedded go.mod as basis for pinning versions
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	if err != nil {
		return fmt.Errorf("parse embedded go.mod: %w", err)
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
		if _, ok := modRequires[minReq.New.Path]; !ok {
			// ignore anything that's sdk/go only
			continue
		}
		genSt.PostCommands = append(genSt.PostCommands,
			exec.Command("go", "mod", "edit", "-replace", minReq.Old.Path+"="+minReq.New.Path+"@"+minReq.New.Version))
		if goWork != "" {
			genSt.PostCommands = append(genSt.PostCommands,
				exec.Command("go", "work", "edit", "-replace", minReq.Old.Path+"="+minReq.New.Path+"@"+minReq.New.Version))
		}
	}

	genSt.PostCommands = append(genSt.PostCommands,
		// run 'go mod tidy' after generating to fix and prune dependencies
		//
		// NOTE: this has to happen before 'go work use' to synchronize Go version
		// bumps
		exec.Command("go", "mod", "tidy"))

	if goWork != "" {
		// run "go work use ." after generating if we had a go.work at the root
		genSt.PostCommands = append(genSt.PostCommands, exec.Command("go", "work", "use", "."))
	}

	return nil
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
		os.Stderr.Write(source)
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	formatted, err = imports.Process(filepath.Join(cfg.OutputDir, "dummy.go"), formatted, nil)
	if err != nil {
		os.Stderr.Write(source)
		return nil, fmt.Errorf("error processing imports in generated code: %w", err)
	}
	return formatted, nil
}

func loadPackage(ctx context.Context, dir string) (_ *packages.Package, _ *token.FileSet, rerr error) {
	ctx, span := trace.Tracer().Start(ctx, "loadPackage")
	defer telemetry.End(span, func() error { return rerr })

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

func goEnv(dir string, env string) (string, error) {
	buf := new(bytes.Buffer)
	findGoWork := exec.Command("go", "env", env)
	findGoWork.Dir = dir
	findGoWork.Stdout = buf
	findGoWork.Stderr = os.Stderr
	if err := findGoWork.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
