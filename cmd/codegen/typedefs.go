package main

import (
	"context"
	"fmt"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"os"
)

type TypeDefFunc func(ctx context.Context) (*generator.GeneratedState, error)

func TypeDefs(ctx context.Context, cfg generator.Config, typedefFunc TypeDefFunc) error {
	logsW := os.Stdout

	_, _ = fmt.Fprintf(logsW, "generating %s typedefs\n", cfg.Lang)

	generated, err := typedefFunc(ctx)
	if err != nil {
		return err
	}

	if err = generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
		return fmt.Errorf("failed to overlay generated code: %w", err)
	}

	if len(generated.PostCommands) > 0 {
		return fmt.Errorf("could not apply post commands for typedefs")
	}

	if generated.NeedRegenerate {
		return fmt.Errorf("could not run a second pass for typedefs")
	}

	return ctx.Err()
}
