package main

import (
	"fmt"
	"log/slog"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"
)

var (
	outputFile string
)

var generateTypeDefsCmd = &cobra.Command{
	Use:  "generate-typedefs",
	RunE: GenerateTypeDefs,
}

func GenerateTypeDefs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)
	defer telemetry.Close()

	cfg, err := getGlobalConfig(ctx, true)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

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
	cfg.TypeDefsPath = outputFile

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
	_ = generateTypeDefsCmd.MarkFlagRequired("module-name")
	_ = generateTypeDefsCmd.MarkFlagRequired("module-source-path")
	generateTypeDefsCmd.Flags().StringVar(&outputFile, "output", "", "path to output file")
}
