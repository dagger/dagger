package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/slog"
)

type TypeDefFunc func(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error)

func TypeDefs(ctx context.Context, cfg generator.Config, typedefFunc TypeDefFunc) error {
	var err error
	var introspectionSchema *introspection.Schema
	var introspectionSchemaVersion string
	if cfg.IntrospectionJSON != "" {
		var resp introspection.Response
		if err := json.Unmarshal([]byte(cfg.IntrospectionJSON), &resp); err != nil {
			return fmt.Errorf("unmarshal introspection json: %w", err)
		}
		introspectionSchema = resp.Schema
		introspectionSchemaVersion = resp.SchemaVersion
	} else {
		introspectionSchema, introspectionSchemaVersion, err = introspection.Introspect(ctx, cfg.Dag)
		if err != nil {
			return err
		}
	}

	// Set the parent schema
	generator.SetSchemaParents(introspectionSchema)

	logsW := os.Stdout

	_, _ = fmt.Fprintf(logsW, "generating %s typedefs\n", cfg.Lang)

	for ctx.Err() == nil {
		generated, err := typedefFunc(ctx, introspectionSchema, introspectionSchemaVersion)
		if err != nil {
			return err
		}

		if err = generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		if generated.NeedRegenerate {
			// Ignoring generated.PostCommands on the last phase:
			// PostCommands are used to perform Go tasks like go mod tidy.
			// Some commands are needed to ensure types are correctly read, but the final ones are not
			// as we don't care about the runnable code, only about types and function signatures.
			for _, cmd := range generated.PostCommands {
				cmd.Dir = cfg.OutputDir
				if cfg.ModuleConfig != nil && cfg.ModuleConfig.ModuleName != "" {
					cmd.Dir = filepath.Join(cfg.OutputDir, cfg.ModuleConfig.ModuleSourcePath)
				}
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				slog.Info("running post-command:", "args", strings.Join(cmd.Args, " "))
				err := cmd.Run()
				if err != nil {
					slog.Error("post-command failed", "error", err)
					return err
				}
			}
			slog.Info("needs another pass...")
			continue
		}

		slog.Info("done!")
		break
	}

	return ctx.Err()
}
