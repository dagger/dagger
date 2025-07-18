package main

import (
	"fmt"
	"log/slog"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/spf13/cobra"
)

var (
	modulePath string
	moduleName string
	isInit     bool
)

var generateModuleCmd = &cobra.Command{
	Use:   "generate-module",
	Short: "Generate a module",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
	RunE: GenerateModule,
}

func GenerateModule(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)

	cfg, err := getGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

	moduleConfig := &generator.ModuleGeneratorConfig{
		IsInit: isInit,
	}

	moduleConfig.ModuleName = moduleName

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

	slog.Info("generating %s module: %s\n", "language", cfg.Lang, "module-name", cfg.ModuleConfig.ModuleName)

	return Generate(ctx, cfg, generator.GenerateModule)
}

func init() {
	// Generation flags
	generateModuleCmd.Flags().StringVar(&lang, "lang", "go", "language to generate")
	generateModuleCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "output directory")
	generateModuleCmd.Flags().StringVar(&introspectionJSONPath, "introspection-json-path", "", "optional path to file containing pre-computed graphql introspection JSON")
	generateModuleCmd.Flags().BoolVar(&bundle, "bundle", false, "generate the client in bundle mode")

	// Specific module generation flags
	generateModuleCmd.Flags().StringVar(&modulePath, "module-source-path", "", "path to source subpath of the module")
	generateModuleCmd.Flags().StringVar(&moduleName, "module-name", "", "name of module to generate code for")
	generateModuleCmd.MarkFlagRequired("module-name")
	generateModuleCmd.MarkFlagRequired("module-source-path")

	generateModuleCmd.Flags().BoolVar(&isInit, "is-init", false, "whether this command is initializing a new module")
}
