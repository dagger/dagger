package gogenerator

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/trace"
	"golang.org/x/tools/go/packages"
)

type PackageInfo struct {
	PackageName   string // Go package name, typically "main"
	PackageImport string // import path of package in which this file appears
}

func loadPackage(ctx context.Context, dir string, allowEmpty bool) (_ *packages.Package, _ *token.FileSet, rerr error) {
	ctx, span := trace.Tracer().Start(ctx, "loadPackage")
	defer telemetry.EndWithCause(span, &rerr)

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
		Logf: func(format string, args ...any) {
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
		if pkgs[0].Name == "" && !allowEmpty {
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
