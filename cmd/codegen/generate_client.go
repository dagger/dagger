package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"

	telemetry "github.com/dagger/otel-go"
)

var (
	//go:embed modsourcedeps.graphql
	loadModuleSourceDepsQuery string
	moduleSourceID            string
	clientDir                 string
	engineVersion             string
	moduleDependencies        string
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
	defer telemetry.Close()

	cfg, err := getGlobalConfig(ctx, false)
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

	switch {
	case moduleName != "":
		clientConfig.ModuleName = moduleName
		clientConfig.EngineVersion = engineVersion
		if moduleDependencies != "" {
			if err := json.Unmarshal([]byte(moduleDependencies), &clientConfig.ModuleDependencies); err != nil {
				return fmt.Errorf("parse --module-dependencies: %w", err)
			}
		}
	case moduleSourceID != "":
		var res struct {
			Source struct {
				Name          string `json:"moduleOriginalName"`
				EngineVersion string `json:"engineVersion"`
				Dependencies  []generator.ModuleSourceDependency
			}
		}

		err := cfg.Dag.Do(ctx,
			&dagger.Request{
				Query:  loadModuleSourceDepsQuery,
				OpName: "ModuleSourceDependencies",
				Variables: map[string]any{
					"source": core.ID(moduleSourceID),
				},
			},
			&dagger.Response{
				Data: &res,
			})
		if err != nil {
			return fmt.Errorf("failed to load module source dependencies: %w", err)
		}

		clientConfig.ModuleName = res.Source.Name
		clientConfig.EngineVersion = res.Source.EngineVersion
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
	generateClientCmd.Flags().StringVar(&moduleName, "module-name", "", "name of the module to generate a client for")
	generateClientCmd.Flags().StringVar(&engineVersion, "engine-version", "", "the module's engine version, which the client's dagger.io/dagger dependency is pinned to")
	generateClientCmd.Flags().StringVar(&moduleDependencies, "module-dependencies", "", "the module's dependencies, as a JSON list of objects with their kind, moduleOriginalName, pin and asString")
	generateClientCmd.Flags().StringVar(&moduleSourceID, "module-source-id", "", "id of the module to generate code for, which codegen loads the name, engine version and dependencies of instead")
	generateClientCmd.Flags().StringVar(&clientDir, "client-dir", "", "directory where the client will be generated (output by default)")
}
