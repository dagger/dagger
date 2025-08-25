package main

import (
	"fmt"
	"log/slog"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"
)

var generateTypeDefsCmd = &cobra.Command{
	Use:  "generate-typedefs",
	RunE: GenerateTypeDefs,
}

func GenerateTypeDefs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)

	cfg, err := getGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

	// ensure we have a dagger connection, this will be required to create type defs
	if cfg.Dag == nil {
		dag, err := dagger.Connect(ctx)
		if err != nil {
			return fmt.Errorf("failed to connect to dagger daemon: %w", err)
		}
		cfg.Dag = dag
	}

	moduleConfig := &generator.ModuleGeneratorConfig{
		ModuleName: moduleName,
	}

	modPath, err := relativeTo(outputDir, modulePath)
	if err != nil {
		return err
	}

	moduleConfig.ModuleSourcePath = modPath
	moduleParentPath, err := relativeTo(modulePath, outputDir)
	if err != nil {
		return err
	}
	moduleConfig.ModuleParentPath = moduleParentPath

	cfg.ModuleConfig = moduleConfig

	generator, err := getGenerator(cfg)
	if err != nil {
		return fmt.Errorf("failed to get generator: %w", err)
	}

	slog.Info("generate type definition", "language", cfg.Lang, "module-name", cfg.ModuleConfig.ModuleName)

	return TypeDefs(ctx, cfg, generator.GenerateTypeDefs)
}

func init() {
	// Specific typedefs generation flags
	generateTypeDefsCmd.Flags().StringVar(&modulePath, "module-source-path", "", "path to source subpath of the module")
	generateTypeDefsCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
	generateTypeDefsCmd.MarkFlagRequired("module-name")
	generateTypeDefsCmd.MarkFlagRequired("module-source-path")
}
