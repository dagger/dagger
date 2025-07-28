package main

import (
	"fmt"
	"log/slog"

	"dagger.io/dagger/telemetry"
	"github.com/spf13/cobra"
)

var generateLibraryCmd = &cobra.Command{
	Use:   "generate-library",
	Short: "Generate the SDK library",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true
	},
	RunE: GenerateLibrary,
}

func GenerateLibrary(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ctx = telemetry.InitEmbedded(ctx, nil)

	cfg, err := getGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get global configuration: %w", err)
	}
	defer cfg.Close()

	generator, err := getGenerator(cfg)
	if err != nil {
		return fmt.Errorf("failed to get generator: %w", err)
	}

	slog.Info("generating SDK library", "language", cfg.Lang)

	return Generate(ctx, cfg, generator.GenerateLibrary)
}
