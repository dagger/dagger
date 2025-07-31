package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

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

	cfg := generator.Config{
		Lang:      generator.SDKLang(lang),
		OutputDir: outputDir,
	}

	moduleConfig := &generator.ModuleGeneratorConfig{}

	if moduleName != "" {
		moduleConfig.ModuleName = moduleName

		if modulePath == "" {
			return fmt.Errorf("--module-name requires --module-source-path")
		}
		modPath, err := relativeTo(outputDir, modulePath)
		if err != nil {
			return err
		}
		if part, _, _ := strings.Cut(modPath, string(filepath.Separator)); part == ".." {
			return fmt.Errorf("module path must be child of output directory")
		}
		moduleConfig.ModuleSourcePath = modPath
		moduleParentPath, err := relativeTo(modulePath, outputDir)
		if err != nil {
			return err
		}
		moduleConfig.ModuleParentPath = moduleParentPath
		//} else {
		//	moduleConfig.ModuleName = filepath.Base(filepath.Clean(moduleConfig.OutputDir))
		//	moduleConfig.ModuleSourcePath = "."
	}

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
}
