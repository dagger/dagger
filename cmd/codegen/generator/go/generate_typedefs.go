package gogenerator

import (
	"context"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dschmidt/go-layerfs"
	"github.com/psanford/memfs"
	"golang.org/x/tools/go/packages"
)

func (g *GoGenerator) GenerateTypeDefs(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	if g.Config.ModuleConfig == nil {
		return nil, fmt.Errorf("generateTypeDefs is called but no typedef config is set")
	}

	moduleConfig := g.Config.ModuleConfig

	if schema != nil {
		generator.SetSchema(schema)
	}

	outDir := filepath.Clean(moduleConfig.ModuleSourcePath)

	mfs := memfs.New()
	var overlay fs.FS = layerfs.New(
		mfs,
	)

	res := &generator.GeneratedState{
		Overlay: overlay,
	}

	pkgInfo, partial, err := g.bootstrapMod(mfs, res, true)
	if err != nil {
		return nil, fmt.Errorf("bootstrap package: %w", err)
	}

	if outDir != "." {
		if err = mfs.MkdirAll(outDir, 0700); err != nil {
			return nil, err
		}
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

	genFile := filepath.Join(g.Config.OutputDir, outDir, "internal/dagger", ClientGenFile)
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
		res.NeedRegenerate = true
		return res, nil
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	if err = generateTypeDefs(ctx, g.Config, mfs, pkg, fset, schema, schemaVersion); err != nil {
		return nil, fmt.Errorf("generate type defs: %w", err)
	}

	return res, nil
}

func generateTypeDefs(ctx context.Context, cfg generator.Config, mfs *memfs.FS, pkg *packages.Package, fset *token.FileSet, schema *introspection.Schema, schemaVersion string) error {
	gen := templates.GoTypeDefsGenerator(ctx, schema, schemaVersion, cfg, pkg, fset, 0)

	t, err := gen.TypeDefs()
	if err != nil {
		return err
	}

	return mfs.WriteFile(cfg.TypeDefsPath, []byte(t), 0600)
}
