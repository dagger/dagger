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

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
)

var clientGenCmd = &cobra.Command{
	Use:  "client-gen",
	RunE: ClientGen,
}

func init() {
	clientGenCmd.Flags().StringP("output", "o", "", "output file")
	clientGenCmd.Flags().String("package", "", "package name")
	clientGenCmd.Flags().String("lang", "", "language to generate in")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	c, err := dagger.Connect(ctx,
		dagger.WithWorkdir(workdir),
		dagger.WithConfigPath(configPath),
		dagger.WithNoExtensions(),
	)
	if err != nil {
		return err
	}
	defer c.Close()

	lang, err := getLang(cmd)
	if err != nil {
		panic(err)
	}

	pkg, err := getPackage(cmd)
	if err != nil {
		panic(err)
	}

	schema, err := generator.Introspect(ctx, c)
	if err != nil {
		panic(err)
	}

	generated, err := generate(ctx, schema, generator.Config{
		Package: pkg,
		Lang:    generator.SDKLang(lang),
	})
	if err != nil {
		return err
	}

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		panic(err)
	}

	if output == "" || output == "-" {
		fmt.Fprint(os.Stdout, string(generated))
	} else {
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(output, generated, 0600); err != nil {
			return err
		}

		gitAttributes := fmt.Sprintf("/%s linguist-generated=true", filepath.Base(output))
		if err := os.WriteFile(path.Join(filepath.Dir(output), ".gitattributes"), []byte(gitAttributes), 0600); err != nil {
			return err
		}
	}

	return nil
}

func getLang(cmd *cobra.Command) (string, error) {
	lang, err := cmd.Flags().GetString("lang")
	if err != nil {
		return "", err
	}

	return lang, nil
}

func getPackage(cmd *cobra.Command) (string, error) {
	pkg, err := cmd.Flags().GetString("package")
	if err != nil {
		return "", err
	}

	// If a package name was provided as a flag, use it
	if pkg != "" {
		return pkg, nil
	}

	// Come up with a default package name
	output, err := cmd.Flags().GetString("output")
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

func generate(ctx context.Context, schema *introspection.Schema, cfg generator.Config) ([]byte, error) {
	generator.SetSchemaParents(schema)

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

	return gen.Generate(ctx, schema)
}
