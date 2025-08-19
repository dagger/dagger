package main

import (
	_ "embed"
	"fmt"
	"log/slog"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"
)

var (
	//go:embed modsourcedeps.graphql
	loadModuleSourceDepsQuery string
	moduleSourceID            string
	clientDir                 string
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

	clientConfig := &generator.ClientGeneratorConfig{
		ClientDir: outputDir,
	}

	// If a client dir is provided, we use it.
	if clientDir != "" {
		clientConfig.ClientDir = clientDir
	}

	if moduleSourceID != "" {
		var res struct {
			Source struct {
				Name         string `json:"moduleOriginalName"`
				Dependencies []generator.ModuleSourceDependency
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

		clientConfig.ModuleName = res.Source.Name
		clientConfig.ModuleDependencies = res.Source.Dependencies
	}

	cfg.ClientConfig = clientConfig

	generator, err := getGenerator(cfg)
	if err != nil {
		return fmt.Errorf("failed to get generator: %w", err)
	}

	slog.Info("generating SDK client", "language", cfg.Lang)

	return Generate(ctx, cfg, generator.GenerateClient)
}

func init() {
	// Specific client generation flags
	generateClientCmd.Flags().StringVar(&moduleSourceID, "module-source-id", "", "id of the module to generate code for")
	generateClientCmd.Flags().StringVar(&clientDir, "client-dir", "", "directory where the client will be generated (output by default)")
}
