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
	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	codegenFlags      = pflag.NewFlagSet("codegen", pflag.ContinueOnError)
	codegenOutputFile string
	codegenPkg        string
)

func init() {
	codegenFlags.StringVarP(&codegenOutputFile, "output", "o", "", "output file")
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

	apiClientOutputPath, err := getAPIClientOutputPath(moduleCfg.SDK)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(apiClientOutputPath)
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

	generated, err := generate(ctx, introspectionSchema, generator.Config{
		Package:             pkg,
		Lang:                generator.SDKLang(moduleCfg.SDK),
		ModuleName:          moduleCfg.Name,
		DependencyModules:   depModules,
		SourceDirectoryPath: parentDir,
	})
	if err != nil {
		return err
	}

	if apiClientOutputPath == "" || apiClientOutputPath == "-" {
		cmd.Println(string(generated.APIClientSource))
		return
	}

	if err := os.WriteFile(apiClientOutputPath, generated.APIClientSource, 0o600); err != nil {
		return err
	}
	defer func() {
		if rerr != nil {
			os.Remove(apiClientOutputPath)
		}
	}()

	gitAttributes := fmt.Sprintf("/%s linguist-generated=true", filepath.Base(apiClientOutputPath))
	gitAttributesPath := path.Join(filepath.Dir(apiClientOutputPath), ".gitattributes")
	if err := os.WriteFile(gitAttributesPath, []byte(gitAttributes), 0o600); err != nil {
		return err
	}
	defer func() {
		if rerr != nil {
			os.Remove(gitAttributesPath)
		}
	}()

	starterTemplateOutputPath, err := getStarterTemplateOutput(moduleCfg.SDK)
	if err != nil {
		return err
	}
	if starterTemplateOutputPath != "" {
		if err := os.WriteFile(starterTemplateOutputPath, generated.StarterTemplateSource, 0o600); err != nil {
			return err
		}
		defer func() {
			if rerr != nil {
				os.Remove(starterTemplateOutputPath)
			}
		}()
	}

	return nil
}

func getAPIClientOutputPath(sdk moduleconfig.SDK) (string, error) {
	if codegenOutputFile != "" {
		return codegenOutputFile, nil
	}

	if sdk != moduleconfig.SDKGo {
		return codegenOutputFile, nil
	}
	modCfg, err := getModuleFlagConfig()
	if err != nil {
		return "", err
	}
	if modCfg.local == nil {
		return codegenOutputFile, nil
	}
	return filepath.Join(filepath.Dir(modCfg.local.path), "dagger.gen.go"), nil
}

func getStarterTemplateOutput(sdk moduleconfig.SDK) (string, error) {
	if sdk != moduleconfig.SDKGo {
		return "", nil
	}
	modCfg, err := getModuleFlagConfig()
	if err != nil {
		return "", err
	}
	if modCfg.local == nil {
		return "", nil
	}
	path := filepath.Join(filepath.Dir(modCfg.local.path), "main.go")
	_, err = os.Stat(path)
	if err == nil {
		return "", nil
	}
	return path, nil
}

func getPackage(sdk moduleconfig.SDK) (string, error) {
	// If a package name was provided as a flag, use it
	if codegenPkg != "" {
		return codegenPkg, nil
	}

	// Come up with a default package name
	output, err := getAPIClientOutputPath(sdk)
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

func generate(ctx context.Context, introspectionSchema *introspection.Schema, cfg generator.Config) (*generator.GeneratedCode, error) {
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
		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
