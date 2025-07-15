package main

import (
	"context"
	"fmt"
	"github.com/dagger/dagger/cmd/codegen/generator"
	gogenerator "github.com/dagger/dagger/cmd/codegen/generator/go"
	typescriptgenerator "github.com/dagger/dagger/cmd/codegen/generator/typescript"
	"os"
)

func TypeDefs(ctx context.Context, cfg generator.Config) error {
	logsW := os.Stdout

	_, _ = fmt.Fprintf(logsW, "generating %s typedefs\n", cfg.Lang)

	generated, err := typeDefs(ctx, cfg)
	if err != nil {
		return err
	}

	if err = generator.Overlay(ctx, logsW, generated.Overlay, cfg.OutputDir); err != nil {
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

func typeDefs(ctx context.Context, cfg generator.Config) (*generator.GeneratedState, error) {
	var gen generator.Generator
	switch cfg.Lang {
	case generator.SDKLangGo:
		gen = &gogenerator.GoGenerator{
			Config: cfg,
		}
	case generator.SDKLangTypeScript:
		gen = &typescriptgenerator.TypeScriptGenerator{
			Config: cfg,
		}
	default:
		sdks := []string{
			string(generator.SDKLangGo),
		}
		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.GenerateTypeDefs(ctx)
}
