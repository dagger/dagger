package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/slog"
)

type TypeDefFunc func(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error)

func TypeDefs(ctx context.Context, cfg generator.Config, typedefFunc TypeDefFunc) error {
	var introspectionSchema *introspection.Schema
	var introspectionSchemaVersion string
	if cfg.IntrospectionJSON != "" {
		var resp introspection.Response
		if err := json.Unmarshal([]byte(cfg.IntrospectionJSON), &resp); err != nil {
			return fmt.Errorf("unmarshal introspection json: %w", err)
		}
		introspectionSchema = resp.Schema
		introspectionSchemaVersion = resp.SchemaVersion

		// Set the parent schema
		generator.SetSchemaParents(introspectionSchema)
	}

	slog.Info("generating %s typedefs\n", cfg.Lang)

	for ctx.Err() == nil {
		generated, err := typedefFunc(ctx, introspectionSchema, introspectionSchemaVersion)
		if err != nil {
			return err
		}

		if err = generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		// Ignoring generated.PostCommands:
		// PostCommands are used to perform Go tasks like go mod tidy.
		// Some commands are needed to ensure types are correctly read, but the final ones are not
		// as we don't care about the runnable code, only about types and function signatures.

		if generated.NeedRegenerate {
			slog.Info("needs another pass...")
			continue
		}

		slog.Info("done!")
		break
	}

	return ctx.Err()
}
