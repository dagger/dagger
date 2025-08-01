package gogenerator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/iancoleman/strcase"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func (g *GoGenerator) GenerateModule(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	if g.Config.ModuleConfig == nil {
		return nil, fmt.Errorf("generateModule is called but module config is missing")
	}

	moduleConfig := g.Config.ModuleConfig

	generator.SetSchema(schema)

	// 1. if no go.mod, generate go.mod
	// 2. if no .go files, bootstrap package main
	//  2a. generate dagger.gen.go from base client,
	//  2b. add stub main.go
	// 3. load package, generate dagger.gen.go (possibly again)

	outDir := filepath.Clean(moduleConfig.ModuleSourcePath)

	mfs := memfs.New()

	var overlay fs.FS = layerfs.New(
		mfs,
		&MountedFS{FS: dagger.QueryBuilder, Name: filepath.Join(outDir, "internal")},
		&MountedFS{FS: dagger.Telemetry, Name: filepath.Join(outDir, "internal")},
	)

	genSt := &generator.GeneratedState{
		Overlay: overlay,
	}

	pkgInfo, partial, err := g.bootstrapMod(mfs, genSt)
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
		if err := mfs.WriteFile(StarterTemplateFile, []byte(baseModuleSource(pkgInfo, moduleConfig.ModuleName)), 0600); err != nil {
			return nil, err
		}

		// main.go is actually an input to codegen, so this requires another pass
		partial = true
	}

	if partial {
		genSt.NeedRegenerate = true
		return genSt, nil
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
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

func (g *GoGenerator) bootstrapMod(mfs *memfs.FS, genSt *generator.GeneratedState) (*PackageInfo, bool, error) {
	moduleConfig := g.Config.ModuleConfig

	var needsRegen bool

	var daggerModPath string
	var goMod *modfile.File

	modname := fmt.Sprintf("dagger/%s", strcase.ToKebab(moduleConfig.ModuleName))
	// check for a go.mod already for the dagger module
	if content, err := os.ReadFile(filepath.Join(g.Config.OutputDir, moduleConfig.ModuleSourcePath, "go.mod")); err == nil {
		daggerModPath = moduleConfig.ModuleSourcePath

		goMod, err = modfile.ParseLax("go.mod", content, nil)
		if err != nil {
			return nil, false, fmt.Errorf("parse go.mod: %w", err)
		}

		if moduleConfig.IsInit && goMod.Module.Mod.Path != modname {
			return nil, false, fmt.Errorf("existing go.mod path %q does not match the module's name %q", goMod.Module.Mod.Path, modname)
		}
	}

	// could not find a go.mod, so we can init a basic one
	if goMod == nil {
		daggerModPath = moduleConfig.ModuleSourcePath
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

	packageImport, err := filepath.Rel(daggerModPath, moduleConfig.ModuleSourcePath)
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

func baseModuleSource(pkgInfo *PackageInfo, moduleName string) string {
	moduleStructName := strcase.ToCamel(moduleName)

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
