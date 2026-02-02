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
		// Only create a root go.mod for legacy/standalone client mode (ClientDir == ".")
		// For separate client modules, we don't create a parent go.mod - just the client's
		if g.Config.ClientConfig.ClientDir == "." {
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
		// For separate client modules without a parent go.mod, continue with nil goModFile
	}

	packageImport := filepath.Join(
		strings.ToLower(g.Config.ClientConfig.ModuleName),
		g.Config.ClientConfig.ClientDir,
	)

	// respect existing package import path if a parent go.mod exists
	if goModFile != nil && goModFile.Module != nil {
		// Find parent go.mod directory and relative path to client
		_, clientRelPath, err := g.findParentGoModDir()
		if err != nil {
			return nil, fmt.Errorf("failed to find parent go.mod: %w", err)
		}

		packageImport = filepath.Join(goModFile.Module.Mod.Path, clientRelPath)
	}

	// Backward compatibility: generate client in root directory using existing go.mod
	if g.Config.ClientConfig.ClientDir == "." {
		// Get the go package from the module
		pkg, _, err := loadPackage(ctx, g.Config.OutputDir, true)
		if err != nil {
			return nil, fmt.Errorf("load package %q: %w", g.Config.OutputDir, err)
		}

		packageName := "dagger"
		if pkg.Module != nil && pkg.Module.Main {
			packageName = "main"
		}

		if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, &PackageInfo{
			PackageName:   packageName,
			PackageImport: packageImport,
		}, nil, nil, 1); err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}

		return &generator.GeneratedState{
			Overlay: layerfs.New(layers...),
			PostCommands: []*exec.Cmd{
				exec.Command("go", "mod", "tidy"),
			},
		}, nil
	}

	// New behavior: create separate go.mod for client in subdirectory
	clientModuleName := packageImport

	// Check if client's go.mod already exists (to know if this is install vs update)
	clientGoModPath := filepath.Join(g.Config.OutputDir, g.Config.ClientConfig.ClientDir, "go.mod")
	_, err = os.Stat(clientGoModPath)
	isInstall := os.IsNotExist(err)

	// Client is always a library package named "dagger"
	packageName := "dagger"

	// Generate the client code first (this creates the directory structure)
	if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, &PackageInfo{
		PackageName:   packageName,
		PackageImport: packageImport,
	}, nil, nil, 1); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	// Now write the client's go.mod (after directory structure is created)
	clientGoMod := new(modfile.File)
	clientGoMod.AddModuleStmt(clientModuleName)
	clientGoMod.AddGoStmt(goVersion)
	// Set dagger.io/dagger version to match the engineVersion from dagger.json
	// Only for released versions (not dev, not empty) - go mod tidy will fail for unreleased versions
	// (replace directives added by tests/users will override this)
	engineVersion := g.Config.ClientConfig.EngineVersion
	if engineVersion != "" && !strings.Contains(engineVersion, "-dev") {
		clientGoMod.AddRequire("dagger.io/dagger", engineVersion)
	}

	clientModBody, err := clientGoMod.Format()
	if err != nil {
		return nil, fmt.Errorf("failed to format client go.mod: %w", err)
	}

	clientGoModFilePath := filepath.Join(g.Config.ClientConfig.ClientDir, "go.mod")
	if err := mfs.WriteFile(clientGoModFilePath, clientModBody, 0600); err != nil {
		return nil, fmt.Errorf("failed to write client go.mod: %w", err)
	}

	// Post commands
	var postCmds []*exec.Cmd
	clientAbsPath := filepath.Join(g.Config.OutputDir, g.Config.ClientConfig.ClientDir)

	// On install (first time), update parent's go.mod with require + replace
	if isInstall && goModFile != nil {
		// Find parent go.mod directory and relative path to client
		parentGoModDir, clientRelPath, err := g.findParentGoModDir()
		if err != nil {
			return nil, fmt.Errorf("failed to find parent go.mod: %w", err)
		}

		// Check if parent go.mod is at OutputDir or above it
		// If at OutputDir, we can write directly to overlay
		// If above OutputDir, we can't write to overlay (would be outside OutputDir), so skip this
		if parentGoModDir == g.Config.OutputDir {
			// Add require and replace to parent's go.mod
			goModFile.AddRequire(clientModuleName, "v0.0.0")
			goModFile.AddReplace(clientModuleName, "", "./"+clientRelPath, "")

			updatedParentGoMod, err := goModFile.Format()
			if err != nil {
				return nil, fmt.Errorf("failed to format parent go.mod: %w", err)
			}

			if err := mfs.WriteFile("go.mod", updatedParentGoMod, 0600); err != nil {
				return nil, fmt.Errorf("failed to write updated parent go.mod: %w", err)
			}
		}
		// If parent go.mod is outside OutputDir, we can't update it via overlay
		// The user will need to manually add require/replace directives

		// Run go mod tidy in the client directory to populate dependencies
		tidyCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %s && go mod tidy", clientAbsPath))
		postCmds = append(postCmds, tidyCmd)

		// Note: We don't run go mod tidy on the parent here because it would remove
		// the require directive if the parent doesn't actually import the client yet.
		// The user should run go mod tidy on the parent when they're ready.
	}

	genSt := &generator.GeneratedState{
		Overlay:      layerfs.New(layers...),
		PostCommands: postCmds,
	}

	return genSt, nil
}

// findParentGoModDir walks up from OutputDir to find the parent go.mod directory
// and returns both the parent directory and the relative path from parent to client.
func (g *GoGenerator) findParentGoModDir() (parentDir string, clientRelPath string, err error) {
	clientAbsPath := filepath.Join(g.Config.OutputDir, g.Config.ClientConfig.ClientDir)
	parentGoModDir := g.Config.OutputDir

	// Walk up to find where the go.mod actually is
	dir := g.Config.OutputDir
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			parentGoModDir = dir
			break
		}
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// Reached root without finding go.mod
			break
		}
		dir = parentDir
	}

	// Get the relative path from parent module to client
	clientRelPath, err = filepath.Rel(parentGoModDir, clientAbsPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to get relative path for client: %w", err)
	}

	return parentGoModDir, clientRelPath, nil
}

func (g *GoGenerator) readGoMod() (*modfile.File, bool, error) {
	// First try to read go.mod from OutputDir
	goModPath := filepath.Join(g.Config.OutputDir, "go.mod")
	goModFile, err := os.ReadFile(goModPath)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// If not found and we're generating a separate client module, look for parent go.mod
		if g.Config.ClientConfig.ClientDir != "." {
			// Walk up the directory tree to find a parent go.mod
			dir := g.Config.OutputDir
			for {
				parentDir := filepath.Dir(dir)
				if parentDir == dir {
					// Reached the root without finding a go.mod
					return nil, false, nil
				}
				dir = parentDir

				parentGoModPath := filepath.Join(dir, "go.mod")
				goModFile, err = os.ReadFile(parentGoModPath)
				if err == nil {
					// Found a parent go.mod
					break
				}
				if !errors.Is(err, os.ErrNotExist) {
					return nil, false, fmt.Errorf("failed to read go.mod at %s: %w", parentGoModPath, err)
				}
			}
		} else {
			// For legacy behavior (ClientDir == "."), no go.mod found
			return nil, false, nil
		}
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("failed to read go.mod: %w", err)
	}

	goMod, err := modfile.Parse("go.mod", goModFile, nil)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	return goMod, true, nil
}
