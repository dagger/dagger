package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

var (
	codegenFlags     = pflag.NewFlagSet("codegen", pflag.ContinueOnError)
	codegenOutputDir string
	codegenPkg       string
)

func init() {
	codegenFlags.StringVarP(&codegenOutputDir, "output", "o", "", "output directory")
	codegenFlags.StringVar(&codegenPkg, "package", "main", "package name")
}

func RunCodegen(
	ctx context.Context,
	engineClient *client.Client,
	_ *dagger.Client,
	moduleCfg *moduleconfig.Config,
	depModules []*dagger.Module,
	cmd *cobra.Command,
	_ []string,
) (rerr error) {
	if workdir != "" {
		if err := os.Chdir(workdir); err != nil {
			return err
		}
	}

	pkg, err := getPackage(moduleCfg.SDK)
	if err != nil {
		return err
	}

	introspectionSchema, err := generator.Introspect(ctx, engineClient)
	if err != nil {
		return err
	}

	outputDir, err := codegenOutDir(moduleCfg.SDK)
	if err != nil {
		return err
	}
	_, parentDirStatErr := os.Stat(outputDir)
	switch {
	case parentDirStatErr == nil:
		// already exists, nothing to do
	case os.IsNotExist(parentDirStatErr):
		// make the parent dir, but if something goes wrong, clean it up in the defer
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}
		defer func() {
			if rerr != nil {
				os.RemoveAll(outputDir)
			}
		}()
	default:
		return fmt.Errorf("failed to stat parent directory: %w", parentDirStatErr)
	}

	generated, postCmds, err := generate(ctx, introspectionSchema, generator.Config{
		Package:             pkg,
		Lang:                generator.SDKLang(moduleCfg.SDK),
		ModuleName:          moduleCfg.Name,
		DependencyModules:   depModules,
		SourceDirectoryPath: outputDir,
	})
	if err != nil {
		return err
	}

	err = fs.WalkDir(generated, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(outputDir, path), 0755)
		}
		r, err := generated.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		defer r.Close()
		w, err := os.Create(filepath.Join(outputDir, path))
		if err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		defer w.Close()
		if _, err := io.Copy(w, r); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to overlay generated code: %w", err)
	}

	rec := progrock.FromContext(ctx)

	for _, cmd := range postCmds {
		cli := strings.Join(cmd.Args, " ")

		vtx := rec.Vertex(digest.FromString(cli), cli)
		cmd.Stdout = vtx.Stdout()
		cmd.Stderr = vtx.Stderr()
		vtx.Done(cmd.Run())
	}

	return nil
}

func codegenOutDir(sdk moduleconfig.SDK) (string, error) {
	if codegenOutputDir != "" {
		return codegenOutputDir, nil
	}
	modCfg, err := getModuleFlagConfig()
	if err != nil {
		return "", err
	}
	if modCfg.local == nil {
		// TODO(vito)
		return ".", nil
	}
	return filepath.Dir(modCfg.local.path), nil
}

func getPackage(sdk moduleconfig.SDK) (string, error) {
	// If a package name was provided as a flag, use it
	if codegenPkg != "" {
		return codegenPkg, nil
	}

	// Come up with a default package name
	output, err := codegenOutDir(sdk)
	if err != nil {
		return "", err
	}

	// If outputting to stdout, use `main` as package
	if output == "" || output == "-" {
		return "main", nil
	}

	directory, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return "", err
	}

	// If outputting to a directory already containing code, use the existing package name.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, directory, nil, parser.PackageClauseOnly)
	if err == nil && len(pkgs) > 0 {
		for _, p := range pkgs {
			return p.Name, nil
		}
	}

	// Otherwise (e.g. outputting to a new directory), use the directory name as package name
	return strings.ToLower(filepath.Base(directory)), nil
}

func generate(ctx context.Context, introspectionSchema *introspection.Schema, cfg generator.Config) (fs.FS, []*exec.Cmd, error) {
	generator.SetSchemaParents(introspectionSchema)

	var gen generator.Generator
	switch cfg.Lang {
	case generator.SDKLangGo:
		gen = &gogenerator.GoGenerator{
			Config: cfg,
		}
	case generator.SDKLangNodeJS:
		gen = &nodegenerator.NodeGenerator{}

	default:
		sdks := []string{
			string(generator.SDKLangGo),
			string(generator.SDKLangNodeJS),
		}
		return nil, nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
