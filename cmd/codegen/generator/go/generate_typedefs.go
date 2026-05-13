package gogenerator

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	codectrace "github.com/dagger/dagger/cmd/codegen/trace"
	telemetry "github.com/dagger/otel-go"
	"github.com/dschmidt/go-layerfs"
	"github.com/psanford/memfs"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/tools/go/packages"
)

func (g *GoGenerator) GenerateTypeDefs(ctx context.Context, schema *introspection.Schema, schemaVersion string) (res *generator.GeneratedState, rerr error) {
	ctx, span := codectrace.Tracer().Start(ctx, "go typegen: generate typedefs")
	defer telemetry.EndWithCause(span, &rerr)

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

	res = &generator.GeneratedState{
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
	genFileExists := true
	if _, err := os.Stat(genFile); err != nil {
		genFileExists = false
	}
	initialGoFilesForLog := summarizePathsForLog(g.Config.OutputDir, initialGoFiles, 20)
	slog.Info("go typegen source scan",
		"module_name", moduleConfig.ModuleName,
		"output_dir", g.Config.OutputDir,
		"module_source_path", moduleConfig.ModuleSourcePath,
		"out_dir", outDir,
		"initial_go_file_count", len(initialGoFiles),
		"initial_go_files", initialGoFilesForLog,
		"dagger_gen_exists", genFileExists,
		"bootstrap_partial", partial,
	)
	recordGoTypegenDiagnosticSpan(ctx,
		fmt.Sprintf("go typegen source scan: module=%s out=%s go_files=%d dagger_gen=%t partial=%t files=%s",
			moduleConfig.ModuleName, outDir, len(initialGoFiles), genFileExists, partial, joinStringsForLog(initialGoFilesForLog)),
		attribute.String("module_name", moduleConfig.ModuleName),
		attribute.String("output_dir", g.Config.OutputDir),
		attribute.String("module_source_path", moduleConfig.ModuleSourcePath),
		attribute.String("out_dir", outDir),
		attribute.Int("initial_go_file_count", len(initialGoFiles)),
		attribute.StringSlice("initial_go_files", initialGoFilesForLog),
		attribute.Bool("initial_go_files_truncated", len(initialGoFilesForLog) < len(initialGoFiles)),
		attribute.Bool("dagger_gen_exists", genFileExists),
		attribute.Bool("bootstrap_partial", partial),
	)
	if !genFileExists {
		// assume package main, default for modules
		pkgInfo.PackageName = "main"
		// generate an initial dagger.gen.go from the base Dagger API
		if err := generateCode(ctx, g.Config, schema, schemaVersion, mfs, pkgInfo, nil, nil, 0); err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}

		slog.Info("go typegen generated initial client",
			"module_name", moduleConfig.ModuleName,
			"module_source_path", moduleConfig.ModuleSourcePath,
			"out_dir", outDir,
			"dagger_gen_path", filepath.ToSlash(filepath.Join(outDir, "internal/dagger", ClientGenFile)),
		)
		genFilePath := filepath.ToSlash(filepath.Join(outDir, "internal/dagger", ClientGenFile))
		recordGoTypegenDiagnosticSpan(ctx,
			fmt.Sprintf("go typegen generated initial client: module=%s path=%s", moduleConfig.ModuleName, genFilePath),
			attribute.String("module_name", moduleConfig.ModuleName),
			attribute.String("module_source_path", moduleConfig.ModuleSourcePath),
			attribute.String("out_dir", outDir),
			attribute.String("dagger_gen_path", genFilePath),
		)
		partial = true
	}

	if len(initialGoFiles) == 0 {
		slog.Info("go typegen writing starter source",
			"module_name", moduleConfig.ModuleName,
			"module_source_path", moduleConfig.ModuleSourcePath,
			"out_dir", outDir,
			"starter_file", StarterTemplateFile,
		)
		recordGoTypegenDiagnosticSpan(ctx,
			fmt.Sprintf("go typegen writing starter source: module=%s file=%s", moduleConfig.ModuleName, StarterTemplateFile),
			attribute.String("module_name", moduleConfig.ModuleName),
			attribute.String("module_source_path", moduleConfig.ModuleSourcePath),
			attribute.String("out_dir", outDir),
			attribute.String("starter_file", StarterTemplateFile),
		)
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

	// Ensure dagger.io/dagger package is available before loading the package
	// if it's not replaced by the user.
	if !pkgInfo.DaggerPkgReplaced {
		if err := g.ensureDaggerPackage(ctx, filepath.Join(g.Config.OutputDir, outDir)); err != nil {
			return nil, fmt.Errorf("ensure dagger package: %w", err)
		}
	}

	pkg, fset, err := loadPackage(ctx, filepath.Join(g.Config.OutputDir, outDir), false)
	if err != nil {
		return nil, fmt.Errorf("load package %q: %w", outDir, err)
	}
	logGoTypegenPackageSummary(ctx, moduleConfig.ModuleName, outDir, pkg, fset)

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

func recordGoTypegenDiagnosticSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	_, span := codectrace.Tracer().Start(ctx, name, oteltrace.WithAttributes(attrs...))
	span.End()
}

func summarizePathsForLog(base string, paths []string, limit int) []string {
	if len(paths) == 0 || limit <= 0 {
		return nil
	}
	relPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(base, p)
		if err != nil {
			rel = p
		}
		relPaths = append(relPaths, filepath.ToSlash(rel))
	}
	sort.Strings(relPaths)
	return limitStringsForLog(relPaths, limit)
}

func limitStringsForLog(vals []string, limit int) []string {
	if len(vals) == 0 || limit <= 0 {
		return nil
	}
	sort.Strings(vals)
	if len(vals) > limit {
		vals = vals[:limit]
	}
	return vals
}

func joinStringsForLog(vals []string) string {
	if len(vals) == 0 {
		return "<none>"
	}
	return strings.Join(vals, ",")
}

func logGoTypegenPackageSummary(ctx context.Context, moduleName, outDir string, pkg *packages.Package, fset *token.FileSet) {
	if pkg == nil {
		return
	}

	var files []string
	var typeNames []string
	var functionNames []string
	var methodNames []string
	for _, file := range pkg.Syntax {
		if file == nil {
			continue
		}
		if fset != nil {
			if pos := fset.Position(file.Pos()); pos.Filename != "" {
				files = append(files, filepath.Base(pos.Filename))
			}
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok != token.TYPE {
					continue
				}
				for _, spec := range decl.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name != nil {
						typeNames = append(typeNames, typeSpec.Name.Name)
					}
				}
			case *ast.FuncDecl:
				if decl.Name == nil {
					continue
				}
				if decl.Recv == nil || len(decl.Recv.List) == 0 {
					functionNames = append(functionNames, decl.Name.Name)
					continue
				}
				recv := receiverNameForLog(decl.Recv.List[0].Type)
				if recv == "" {
					recv = "<unknown>"
				}
				methodNames = append(methodNames, recv+"."+decl.Name.Name)
			}
		}
	}

	filesForLog := limitStringsForLog(files, 20)
	typeNamesForLog := limitStringsForLog(typeNames, 40)
	functionNamesForLog := limitStringsForLog(functionNames, 40)
	methodNamesForLog := limitStringsForLog(methodNames, 60)

	slog.Info("go typegen package summary",
		"module_name", moduleName,
		"out_dir", outDir,
		"package_name", pkg.Name,
		"syntax_file_count", len(pkg.Syntax),
		"syntax_files", filesForLog,
		"type_count", len(typeNames),
		"type_names", typeNamesForLog,
		"function_count", len(functionNames),
		"function_names", functionNamesForLog,
		"method_count", len(methodNames),
		"method_names", methodNamesForLog,
	)
	recordGoTypegenDiagnosticSpan(ctx,
		fmt.Sprintf("go typegen package summary: module=%s pkg=%s files=%d types=%d funcs=%d methods=%d method_names=%s",
			moduleName, pkg.Name, len(pkg.Syntax), len(typeNames), len(functionNames), len(methodNames), joinStringsForLog(limitStringsForLog(append([]string(nil), methodNamesForLog...), 12))),
		attribute.String("module_name", moduleName),
		attribute.String("out_dir", outDir),
		attribute.String("package_name", pkg.Name),
		attribute.Int("syntax_file_count", len(pkg.Syntax)),
		attribute.StringSlice("syntax_files", filesForLog),
		attribute.Bool("syntax_files_truncated", len(filesForLog) < len(files)),
		attribute.Int("type_count", len(typeNames)),
		attribute.StringSlice("type_names", typeNamesForLog),
		attribute.Bool("type_names_truncated", len(typeNamesForLog) < len(typeNames)),
		attribute.Int("function_count", len(functionNames)),
		attribute.StringSlice("function_names", functionNamesForLog),
		attribute.Bool("function_names_truncated", len(functionNamesForLog) < len(functionNames)),
		attribute.Int("method_count", len(methodNames)),
		attribute.StringSlice("method_names", methodNamesForLog),
		attribute.Bool("method_names_truncated", len(methodNamesForLog) < len(methodNames)),
	)
}

func receiverNameForLog(expr ast.Expr) string {
	name := types.ExprString(expr)
	name = strings.TrimPrefix(name, "*")
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// We need the dagger package to be installed to correctly resolved imported interface
// such as `querybuilder.GraphQLMarshaller`
func (g *GoGenerator) ensureDaggerPackage(ctx context.Context, dir string) error {
	if g.Config.ModuleConfig == nil || g.Config.ModuleConfig.LibVersion == "" {
		return nil
	}

	cmd := exec.CommandContext(ctx, "go", "get", daggerImportPath+"@"+g.Config.ModuleConfig.LibVersion)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get %s@%s: %w", daggerImportPath, g.Config.ModuleConfig.LibVersion, err)
	}

	return nil
}
