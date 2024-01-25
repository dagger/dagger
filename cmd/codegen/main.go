package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/core/modules"
)

var (
	outputDir             string
	moduleRef             string
	lang                  string
	introspectionJSONPath string
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
	rootCmd.Flags().StringVar(&moduleRef, "module", "", "module to load and codegen dependency code")
	rootCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")
}

func ClientGen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	cfg := generator.Config{
		Lang: generator.SDKLang(lang),

		OutputDir: outputDir,
	}

	if moduleRef != "" {
		ref, err := modules.ResolveMovingRef(ctx, dag, moduleRef)
		if err != nil {
			return fmt.Errorf("resolve module ref: %w", err)
		}

		modCfg, err := ref.Config(ctx, dag)
		if err != nil {
			return fmt.Errorf("load module config: %w", err)
		}

		cfg.ModuleRef = ref
		cfg.ModuleConfig = modCfg
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
