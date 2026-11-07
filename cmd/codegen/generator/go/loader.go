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
	"os"
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
	cfg := &packages.Config{
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
	}
	driverLog, err := exportDriverLog(cfg)
	if err != nil {
		return nil, nil, err
	}
	if driverLog != "" {
		defer os.Remove(driverLog)
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, nil, err
	}
	if driverLog != "" {
		if err := checkExportDriver(driverLog); err != nil {
			return nil, nil, err
		}
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

// Environment variables of golangci-lint's export driver, which the Go SDK's
// runtime sets for codegen to load packages through it.
const (
	// envExportArchives is the directory of the prebuilt archives the
	// driver serves the export data of the module's imports from.
	envExportArchives = "GOLANGCI_EXPORT_ARCHIVES"
	// envExportDriverLog is a file the driver appends a JSON line to for
	// each request: whether it declined, and why.
	envExportDriverLog = "GOLANGCI_EXPORT_DRIVER_LOG"
)

// exportDriverLog sets cfg up for the export driver to report to a new file,
// whose path it returns, when codegen loads packages through the driver: ""
// otherwise.
func exportDriverLog(cfg *packages.Config) (string, error) {
	if os.Getenv(envExportArchives) == "" {
		return "", nil
	}
	f, err := os.CreateTemp("", "export-driver-*.log")
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	cfg.Env = append(os.Environ(), envExportDriverLog+"="+f.Name())
	return f.Name(), nil
}

// checkExportDriver fails if the export driver declined a request or didn't
// report on it. A driver that declines leaves go/packages to compile every
// package the module imports, from source, and says nothing: codegen still
// works, many times slower, on every load. The archives are built for this
// load right before it, so a decline means they and the driver disagree:
// the driver and the archives' manifest writer are at different manifest
// formats, or the archives were built with another Go toolchain, platform or
// build tags.
func checkExportDriver(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the export driver's report: %w", err)
	}
	served := false
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var rep struct {
			Declined string `json:"declined"`
		}
		if err := dec.Decode(&rep); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("read the export driver's report: %w", err)
		}
		if rep.Declined != "" {
			return fmt.Errorf("the Go SDK's export driver declined to serve codegen from the prebuilt archives: %s. "+
				"Codegen stops rather than compile every package the module imports from source, which is many times slower. "+
				"This is a bug in the Go SDK, please report it: the driver and the archives must agree on the manifest format "+
				"(the engine's GolangciLintCommit and dagger/go's pin of golangci-lint), the Go version, the platform and the build tags",
				rep.Declined)
		}
		served = true
	}
	if !served {
		return fmt.Errorf("the Go SDK's export driver didn't serve codegen from the prebuilt archives (GOPACKAGESDRIVER=%q): "+
			"codegen stops rather than compile every package the module imports from source. This is a bug in the Go SDK, please report it",
			os.Getenv("GOPACKAGESDRIVER"))
	}
	return nil
}
