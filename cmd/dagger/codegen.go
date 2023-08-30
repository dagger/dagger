package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/core/envconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	codegenFlags  = pflag.NewFlagSet("codegen", pflag.ContinueOnError)
	codegenOutput string
	codegenPkg    string
)

func init() {
	codegenFlags.StringVarP(&codegenOutput, "output", "o", "", "output file")
	codegenFlags.StringVar(&codegenPkg, "package", "main", "package name")
}

func RunCodegen(
	ctx context.Context,
	engineClient *client.Client,
	_ *dagger.Client,
	envCfg *envconfig.Config,
	depEnvs []*dagger.Environment,
	cmd *cobra.Command,
	_ []string,
) (rerr error) {
	if workdir != "" {
		if err := os.Chdir(workdir); err != nil {
			return err
		}
	}

	pkg, err := getPackage(envCfg.SDK)
	if err != nil {
		return err
	}

	introspectionSchema, err := generator.Introspect(ctx, engineClient)
	if err != nil {
		return err
	}

	generated, err := generate(ctx, introspectionSchema, generator.Config{
		Package:                pkg,
		Lang:                   generator.SDKLang(envCfg.SDK),
		EnvironmentName:        envCfg.Name,
		DependencyEnvironments: depEnvs,
	})
	if err != nil {
		return err
	}

	output, err := getOutput(envCfg.SDK)
	if err != nil {
		return err
	}

	if output == "" || output == "-" {
		cmd.Println(string(generated))
	} else {
		parentDir := filepath.Dir(output)
		_, parentDirStatErr := os.Stat(parentDir)
		switch {
		case parentDirStatErr == nil:
			// already exists, nothing to do
		case os.IsNotExist(parentDirStatErr):
			// make the parent dir, but if something goes wrong, clean it up in the defer
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %w", err)
			}
			defer func() {
				if rerr != nil {
					os.RemoveAll(parentDir)
				}
			}()
		default:
			return fmt.Errorf("failed to stat parent directory: %w", parentDirStatErr)
		}

		if err := os.WriteFile(output, generated, 0o600); err != nil {
			return err
		}
		defer func() {
			if rerr != nil {
				os.Remove(output)
			}
		}()

		gitAttributes := fmt.Sprintf("/%s linguist-generated=true", filepath.Base(output))
		gitAttributesPath := path.Join(filepath.Dir(output), ".gitattributes")
		if err := os.WriteFile(gitAttributesPath, []byte(gitAttributes), 0o600); err != nil {
			return err
		}
		defer func() {
			if rerr != nil {
				os.Remove(gitAttributesPath)
			}
		}()
	}

	return nil
}

func getOutput(sdk envconfig.SDK) (string, error) {
	if codegenOutput != "" {
		return codegenOutput, nil
	}

	// TODO:
	if sdk != envconfig.SDKGo {
		return codegenOutput, nil
	}
	envCfg, err := getEnvironmentFlagConfig()
	if err != nil {
		return "", err
	}
	if envCfg.local == nil {
		return codegenOutput, nil
	}
	return filepath.Join(filepath.Dir(envCfg.local.path), "dagger.gen.go"), nil
}

func getPackage(sdk envconfig.SDK) (string, error) {
	// If a package name was provided as a flag, use it
	if codegenPkg != "" {
		return codegenPkg, nil
	}

	// Come up with a default package name
	output, err := getOutput(sdk)
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

func generate(ctx context.Context, introspectionSchema *introspection.Schema, cfg generator.Config) ([]byte, error) {
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
		return []byte{}, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
