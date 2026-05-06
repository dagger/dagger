package main

import (
	"fmt"
	"log/slog"

	"github.com/dagger/dagger/cmd/codegen/generator"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

var (
	entrypointTypedefPath string
	entrypointOutputFile  string
	entrypointModuleRoot  string
	entrypointSDKImport   string
	entrypointSourceDir   string
)

var generateEntrypointCmd = &cobra.Command{
	Use:   "generate-entrypoint",
	Short: "Render a module's static dispatch entrypoint from a typedef JSON file",
	RunE:  GenerateEntrypoint,
}

func GenerateEntrypoint(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)
	defer telemetry.Close()

	cfg, err := getGlobalConfig(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

	cfg.EntrypointConfig = &generator.EntrypointGeneratorConfig{
		TypedefJSONPath: entrypointTypedefPath,
		OutputFile:      entrypointOutputFile,
		ModuleRoot:      entrypointModuleRoot,
		SDKImportPath:   entrypointSDKImport,
		SourceDir:       entrypointSourceDir,
	}

	gen, err := getGenerator(cfg)
	if err != nil {
		return fmt.Errorf("failed to get generator: %w", err)
	}

	slog.Info("generate module entrypoint", "language", cfg.Lang, "typedef-json-path", entrypointTypedefPath)

	return Entrypoint(ctx, cfg, gen.GenerateEntrypoint)
}

func init() {
	generateEntrypointCmd.Flags().StringVar(&entrypointTypedefPath, "typedef-json-path", "", "path to the typedef JSON produced by the SDK introspector")
	generateEntrypointCmd.Flags().StringVar(&entrypointOutputFile, "output-file", "", "filename to write the entrypoint to within the output directory (defaults to the SDK's standard filename)")
	generateEntrypointCmd.Flags().StringVar(&entrypointModuleRoot, "module-root", "", "absolute path of the user's module root (used to resolve relative source-import paths)")
	generateEntrypointCmd.Flags().StringVar(&entrypointSDKImport, "sdk-import", "", "bare specifier the entrypoint uses to import runtime helpers (defaults to the SDK's standard package name)")
	generateEntrypointCmd.Flags().StringVar(&entrypointSourceDir, "source-dir", "", "user's source directory name relative to module root (defaults to \"src\")")
	_ = generateEntrypointCmd.MarkFlagRequired("typedef-json-path")
}
