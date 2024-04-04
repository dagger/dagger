package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
)

var (
	outputDir             string
	lang                  string
	introspectionJSONPath string

	moduleContextPath string
	moduleName        string
)

var rootCmd = &cobra.Command{
	Use:  "codegen",
	RunE: ClientGen,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
}

func init() {
	rootCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	rootCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")

	rootCmd.Flags().StringVar(&moduleContextPath, "module-context", "", "path to context directory of the module")
	rootCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx, dagger.WithSkipCompatibilityCheck())
	if err != nil {
		return err
	}

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,
	}

	if moduleName != "" {
		cfg.ModuleName = moduleName

		if moduleContextPath == "" {
			return fmt.Errorf("--module-name requires --module-context")
		}
		cfg.ModuleContextPath = moduleContextPath
	}

	if introspectionJSONPath != "" {
		introspectionJSON, err := os.ReadFile(introspectionJSONPath)
		if err != nil {
			return fmt.Errorf("read introspection json: %w", err)
		}
		cfg.IntrospectionJSON = string(introspectionJSON)
	}

	return Generate(ctx, cfg, dag)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
