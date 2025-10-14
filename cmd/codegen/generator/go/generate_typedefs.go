package gogenerator

import (
	"context"
	"fmt"
	"go/token"
	"io/fs"
	"path/filepath"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/psanford/memfs"
	"golang.org/x/tools/go/packages"
)

const (
	// TypeDefsFile is the path to write the type definitions for the module
	TypeDefsFile = "typedefs.json"
)

func (g *GoGenerator) GenerateTypeDefs(ctx context.Context) (*generator.GeneratedState, error) {
	if g.Config.TypeDefGeneratorConfig == nil {
		return nil, fmt.Errorf("generateTypeDefs is called but no typedef config is set")
	}

	typeDefConfig := g.Config.TypeDefGeneratorConfig

	outDir := "."
	if typeDefConfig.ModuleName != "" {
		outDir = filepath.Clean(typeDefConfig.ModuleSourcePath)
	}

	mfs := memfs.New()
	var overlay fs.FS = mfs

	res := &generator.GeneratedState{
		Overlay: overlay,
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	if err = generateTypeDefs(ctx, g.Config, mfs, pkg, fset); err != nil {
		return nil, fmt.Errorf("generate type defs: %w", err)
	}

	return res, nil
}

func generateTypeDefs(ctx context.Context, cfg generator.Config, mfs *memfs.FS, pkg *packages.Package, fset *token.FileSet) error {
	gen := templates.GoTypeDefsGenerator(ctx, nil, "", cfg, pkg, fset, 0)

	t, err := gen.TypeDefs()
	if err != nil {
		return err
	}

	return mfs.WriteFile(TypeDefsFile, []byte(t), 0600)
}
