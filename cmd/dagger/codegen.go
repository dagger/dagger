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
	"github.com/dagger/dagger/core/environmentconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// TODO: converge client-gen binary to this one too
var codegenCmd = &cobra.Command{
	Use:  "codegen",
	RunE: loadEnvDepsCmdWrapper(RunCodegen),
}

func init() {
	codegenCmd.Flags().StringP("output", "o", "", "output file")
	codegenCmd.Flags().String("package", "", "package name")
}

func RunCodegen(
	ctx context.Context,
	engineClient *client.Client,
	c *dagger.Client,
	envCfg *environmentconfig.Config,
	depEnvs []*dagger.Environment,
	cmd *cobra.Command,
	_ []string,
) error {
	if workdir != "" {
		if err := os.Chdir(workdir); err != nil {
			return err
		}
	}

	pkg, err := getPackage(cmd, envCfg.SDK)
	if err != nil {
		return err
	}

	introspectionSchema, err := generator.Introspect(ctx, engineClient)
	if err != nil {
		return err
	}

	generated, err := generate(ctx, introspectionSchema, generator.Config{
		Package:      pkg,
		Lang:         generator.SDKLang(envCfg.SDK),
		Environments: depEnvs,
	})
	if err != nil {
		return err
	}

	output, err := getOutput(cmd, envCfg.SDK)
	if err != nil {
		return err
	}

	if output == "" || output == "-" {
		cmd.Println(string(generated))
	} else {
		if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(output, generated, 0o600); err != nil {
			return err
		}

		gitAttributes := fmt.Sprintf("/%s linguist-generated=true", filepath.Base(output))
		if err := os.WriteFile(path.Join(filepath.Dir(output), ".gitattributes"), []byte(gitAttributes), 0o600); err != nil {
			return err
		}
	}

	return nil
}

func getOutput(cmd *cobra.Command, sdk environmentconfig.SDK) (string, error) {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return "", err
	}
	if output != "" {
		return output, nil
	}

	// TODO:
	if sdk != environmentconfig.SDKGo {
		return output, nil
	}
	envCfg, err := getEnvironmentFlagConfig()
	if err != nil {
		return "", err
	}
	if envCfg.local == nil {
		return output, nil
	}
	return filepath.Join(filepath.Dir(envCfg.local.path), "dagger.gen.go"), nil
}

func getPackage(cmd *cobra.Command, sdk environmentconfig.SDK) (string, error) {
	pkg, err := cmd.Flags().GetString("package")
	if err != nil {
		return "", err
	}

	// If a package name was provided as a flag, use it
	if pkg != "" {
		return pkg, nil
	}

	// Come up with a default package name
	output, err := getOutput(cmd, sdk)
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
