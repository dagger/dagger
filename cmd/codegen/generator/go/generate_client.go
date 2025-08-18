package gogenerator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/psanford/memfs"
	"golang.org/x/mod/modfile"
)

func (g *GoGenerator) GenerateClient(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	mfs := memfs.New()

	layers := []fs.FS{mfs}

	goModFile, exist, err := g.readGoMod()
	if err != nil {
		return nil, fmt.Errorf("failed to read go.mod: %w", err)
	}

	if !exist {
		// If no go.mod is found, we will generate a go.mod
		goMod := new(modfile.File)
		goMod.AddModuleStmt(strings.ToLower(g.Config.ClientConfig.ModuleName))
		goMod.AddGoStmt(goVersion)

		modBody, err := goMod.Format()
		if err != nil {
			return nil, fmt.Errorf("failed to format go.mod: %w", err)
		}

		if err := mfs.WriteFile("go.mod", modBody, 0600); err != nil {
			return nil, fmt.Errorf("failed to write go.mod: %w", err)
		}

		return &generator.GeneratedState{
			Overlay:        layerfs.New(mfs),
			NeedRegenerate: true,
		}, nil
	}

	replacedPath, replaced, err := g.daggerPackageReplacement(goModFile)
	if err != nil {
		return nil, fmt.Errorf("failed to check if dagger.io/dagger package is replaced: %w", err)
	}

	// If dagger.io/dagger package is replaced, we need to add the SDK locally
	if replaced {
		layers = append(
			layers,
			&MountedFS{FS: dagger.GoSDK, Name: replacedPath},
		)
	}

	// Get the go package from the module
	// We assume that we'll be located at the root source directory
	// If no main package is found, we will generat the client as a sub module library.
	pkg, _, err := loadPackage(ctx, g.Config.OutputDir, true)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", g.Config.OutputDir, err)
	}

	packageImport := filepath.Join(
		strings.ToLower(g.Config.ClientConfig.ModuleName),
		g.Config.ClientConfig.ClientDir,
	)

	// respect existing package import path if a package is set
	if goModFile.Module != nil {
		packageImport = filepath.Join(goModFile.Module.Mod.Path, g.Config.ClientConfig.ClientDir)
	}

	genSt := &generator.GeneratedState{
		Overlay: layerfs.New(layers...),
		PostCommands: []*exec.Cmd{
			exec.Command("go", "mod", "tidy"),
		},
	}

	packageName := "dagger"
	if pkg.Module != nil && pkg.Module.Main && g.Config.ClientConfig.ClientDir == "." {
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

func (g *GoGenerator) daggerPackageReplacement(goMod *modfile.File) (string, bool, error) {
	for _, replace := range goMod.Replace {
		if replace.Old.Path == "dagger.io/dagger" {
			// We need to exclude the first parent directory of the replaced path since it's the
			// root of the generated directory (c.Config.OutputDir) and the overlays root is that
			// path.
			// FIXME(TomChv): This will disapear once I fix the overlays root to the module root instead
			// of the client output directory.
			replacedPath := replace.New.Path

			if filepath.IsAbs(replacedPath) {
				return "", false, fmt.Errorf("invalid go replace path %q not under %q", replacedPath, g.Config.OutputDir)
			}

			// Remove the output dir from the replace path and trim the leading slash to obtain
			// a local path usable on overlay
			// Example:
			// - ./dagger/sdk generated in dagger -> sdk)
			// - ./sdk generated in . -> sdk)
			// - ./dagger/foo/bar generated in dagger -> foo/bar)
			replacedPath = strings.TrimPrefix(
				strings.TrimPrefix(filepath.Clean(replacedPath), filepath.Clean(g.Config.OutputDir)),
				"/",
			)

			return replacedPath, true, nil
		}
	}

	return "", false, nil
}

func (g *GoGenerator) readGoMod() (*modfile.File, bool, error) {
	goModFile, err := os.ReadFile(filepath.Join(g.Config.OutputDir, "go.mod"))
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to read go.mod: %w", err)
	}

	goMod, err := modfile.Parse("go.mod", goModFile, nil)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	return goMod, true, nil
}
