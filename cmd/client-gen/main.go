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

	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/tracing"
)

var (
	configPath string
	workdir    string
)

var rootCmd = &cobra.Command{
	Use:  "client-gen",
	RunE: ClientGen,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "project config file")
	rootCmd.Flags().StringP("output", "o", "", "output file")
	rootCmd.Flags().String("package", "", "package name")
	rootCmd.Flags().String("lang", "", "language to generate in")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	engineConf := &engine.Config{
		Workdir:      workdir,
		ConfigPath:   configPath,
		RunnerHost:   internalengine.RunnerHost(),
		NoExtensions: true,
	}
	return engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		lang, err := getLang(cmd)
		if err != nil {
			return err
		}

		pkg, err := getPackage(cmd)
		if err != nil {
			return err
		}

		schema, err := generator.Introspect(ctx, r)
		if err != nil {
			return err
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
			return err
		}

		if output == "" || output == "-" {
			fmt.Fprint(os.Stdout, string(generated))
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
	})
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

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}
