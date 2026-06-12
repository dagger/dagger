package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/engine/slog"
)

// EntrypointFunc matches the signature of Generator.GenerateEntrypoint.
type EntrypointFunc func(ctx context.Context) (*generator.GeneratedState, error)

// Entrypoint runs the entrypoint generator and writes the resulting overlay
// to the configured output directory. Mirrors `TypeDefs` for the typedef
// generator — there's no introspection schema involved; the input is a
// typedef JSON file referenced from `cfg.EntrypointConfig.TypedefJSONPath`.
func Entrypoint(ctx context.Context, cfg generator.Config, fn EntrypointFunc) error {
	slog.Info(fmt.Sprintf("generating %s module entrypoint", cfg.Lang))

	generated, err := fn(ctx)
	if err != nil {
		return err
	}

	if err := generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
		return fmt.Errorf("failed to overlay generated entrypoint: %w", err)
	}

	slog.Info("done!")
	return nil
}
