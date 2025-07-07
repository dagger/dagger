package main

import (
	_ "embed"
	"fmt"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"
)

var (
	//go:embed modsourcedeps.graphql
	loadModuleSourceDepsQuery string
	moduleSourceID string
)

var generateClientCmd = &cobra.Command{
	Use:   "generate-client",
	Short: "Generate a client",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
	RunE: GenerateClient,
}

func GenerateClient(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)

	cfg, err := getGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

	clientConfig := &generator.ClientGeneratorConfig{}

	if moduleSourceID != "" {
		var res struct {
			Source struct {
				Dependencies []generator.ModuleSourceDependencies
			}
		}

		err := cfg.Dag.Do(ctx,
			&dagger.Request{
				Query:  loadModuleSourceDepsQuery,
				OpName: "ModuleSourceDependencies",
				Variables: map[string]any{
					"source": dagger.ModuleSourceID(moduleSourceID),
				},
			},
			&dagger.Response{
				Data: &res,
			})
		if err != nil {
			return fmt.Errorf("failed to load module source dependencies: %w", err)
		}

		clientConfig.ModuleDependencies = res.Source.Dependencies
	}

	cfg.ClientConfig = clientConfig

	return Generate(ctx, cfg)

}

func init() {
	// Generation flags
	generateClientCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	generateClientCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	generateClientCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")
	generateClientCmd.Flags().BoolVar(&bundle, "bundle", false, "generate the client in bundle mode")

	// Specific client generation flags
	generateClientCmd.Flags().StringVar(&moduleSourceID, "module-source-id", "", "id of the module to generate code for")
}
