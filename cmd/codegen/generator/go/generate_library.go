package gogenerator

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/psanford/memfs"
)

func (g *GoGenerator) GenerateLibrary(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)
	outDir := "."
	mfs := memfs.New()
	var overlay fs.FS = mfs

	genSt := &generator.GeneratedState{
		Overlay: overlay,
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}

	if err := generateCode(ctx,
		g.Config, schema, schemaVersion, mfs,
		&PackageInfo{
			PackageName:   pkg.Name,
			PackageImport: pkg.Module.Path,
		},
		pkg, fset, 1,
	); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}

	return genSt, nil
}
