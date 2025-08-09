package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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

		// Ignoring generated.PostCommands:
		// PostCommands are used to perform Go tasks like go mod tidy.
		// This is not needed for typedefs and can also fail as the types we want to make available might not yet
		// been generated (because they require... the types definition this function is creating)
		// In case of an init (an empty module) the typedefFunc in argument will create a default module
		// without worrying about code generation. Just the basic main.go and go.mod files. Those files will not
		// be exposed to the user, the codegen phase will then initialize a full module, but at this time
		// with a schema containing all the types, including the module itself.

		if !generated.NeedRegenerate {
			slog.Info("done!")
			break
		}

		slog.Info("needs another pass...")
	}

	return ctx.Err()
}
