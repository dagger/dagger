package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/moby/buildkit/identity"
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

	rec := progrock.FromContext(ctx)

	for {
		generated, err := generate(ctx, introspectionSchema, generator.Config{
			Package:             pkg,
			Lang:                generator.SDKLang(moduleCfg.SDK),
			ModuleName:          moduleCfg.Name,
			DependencyModules:   depModules,
			SourceDirectoryPath: outputDir,
		})
		if err != nil {
			return err
		}

		if err := generator.Overlay(ctx, generated.Overlay, outputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		// TODO: move this into generator package, share with cmd/client-gen/. or
		// just delete cmd/client-gen/?
		for _, cmd := range generated.PostCommands {
			cli := strings.Join(cmd.Args, " ")

			vtx := rec.Vertex(digest.FromString(identity.NewID()), cli)
			cmd.Stdout = vtx.Stdout()
			cmd.Stderr = vtx.Stderr()
			vtx.Done(cmd.Run())
		}

		if !generated.NeedRegenerate {
			break
		}
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

func generate(ctx context.Context, introspectionSchema *introspection.Schema, cfg generator.Config) (*generator.GeneratedState, error) {
	generator.SetSchemaParents(introspectionSchema)

	var gen generator.Generator
	switch cfg.Lang {
	case generator.SDKLangGo:
		gen = &gogenerator.GoGenerator{
			Config: cfg,
		}
	case generator.SDKLangNodeJS:
		gen = &nodegenerator.NodeGenerator{
			Config: cfg,
		}

	default:
		sdks := []string{
			string(generator.SDKLangGo),
			string(generator.SDKLangNodeJS),
		}
		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
