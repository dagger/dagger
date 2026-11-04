package gogenerator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/trace"
	telemetry "github.com/dagger/otel-go"
	"golang.org/x/tools/go/packages"
)

type PackageInfo struct {
	PackageName       string // Go package name, typically "main"
	PackageImport     string // import path of package in which this file appears
	DaggerPkgReplaced bool   // True `dagger.io/dagger` is replaced.
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
		// NeedExportFile asks for what the go command lists with
		// NeedTypes alone, the export data of what the package imports, but
		// asks a packages driver for it too, such as the Go SDK's export
		// driver, which otherwise leaves it out.
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedModule |
			packages.NeedExportFile,
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
		// A package loaded through a GOPACKAGESDRIVER, such as the Go SDK's
		// export driver, comes without its module, which go/packages doesn't
		// pass on from a driver: the go command lists it.
		if pkgs[0].Module == nil && pkgs[0].Name != "" {
			mod, err := listModule(ctx, dir)
			if err != nil {
				return nil, nil, fmt.Errorf("module of %s: %w", dir, err)
			}
			pkgs[0].Module = mod
		}
		return pkgs[0], fset, nil
	default:
		// this would mean I don't understand how loading '.' works
		return nil, nil, fmt.Errorf("expected 1 package, got %d", len(pkgs))
	}
}

// listModule is the module the directory dir is in, as "go list -m" lists
// it: in a go.work, the workspace module whose directory holds dir.
func listModule(ctx context.Context, dir string) (*packages.Module, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var best *packages.Module
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m packages.Module
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list -m: %w", err)
		}
		if rel, err := filepath.Rel(m.Dir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			if best == nil || len(m.Dir) > len(best.Dir) {
				best = &m
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("go list -m lists no module holding %s", abs)
	}
	return best, nil
}
