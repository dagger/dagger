package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	gogenerator "github.com/dagger/dagger/cmd/codegen/generator/go"
	typescriptgenerator "github.com/dagger/dagger/cmd/codegen/generator/typescript"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/trace"
	telemetry "github.com/dagger/otel-go"
)

type GenFunc func(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error)

func getGenerator(cfg generator.Config) (generator.Generator, error) {
	switch cfg.Lang {
	case generator.SDKLangGo:
		return &gogenerator.GoGenerator{
			Config: cfg,
		}, nil
	case generator.SDKLangTypeScript:
		return &typescriptgenerator.TypeScriptGenerator{
			Config: cfg,
		}, nil

	default:
		sdks := []string{
			string(generator.SDKLangGo),
			string(generator.SDKLangTypeScript),
		}

		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}
}

func Generate(ctx context.Context, cfg generator.Config, genFunc GenFunc) (err error) {
	introspectionSchema, introspectionSchemaVersion, err := loadSchema(ctx, cfg)
	if err != nil {
		return err
	}

	for ctx.Err() == nil {
		generated, err := genFunc(ctx, introspectionSchema, introspectionSchemaVersion)
		if err != nil {
			return err
		}

		if err := applyGenerated(ctx, generated, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to apply generated code: %w", err)
		}

		for _, cmd := range generated.PostCommands {
			// Only set cmd.Dir if the command hasn't already specified one.
			// This allows generators to target a specific sub-directory (e.g.
			// the client module directory) without being overridden here.
			if cmd.Dir == "" {
				cmd.Dir = cfg.OutputDir
				if cfg.ModuleConfig != nil && cfg.ModuleConfig.ModuleName != "" {
					cmd.Dir = filepath.Join(cfg.OutputDir, cfg.ModuleConfig.ModuleSourcePath)
				}
			}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			slog.Info("running post-command:", "args", strings.Join(cmd.Args, " "))
			err := runPostCommand(ctx, cmd)
			if err != nil {
				slog.Error("post-command failed", "error", err)
				return err
			}
		}

		if !generated.NeedRegenerate {
			slog.Info("done!")
			break
		}
		if cfg.ModuleConfig != nil && cfg.ModuleConfig.Bootstrap {
			slog.Info("bootstrapped")
			break
		}

		slog.Info("needs another pass...")
	}

	return ctx.Err()
}

// loadSchema is the schema code is generated from: the introspection JSON
// codegen was given, or the engine's.
func loadSchema(ctx context.Context, cfg generator.Config) (_ *introspection.Schema, _ string, rerr error) {
	ctx, span := trace.Tracer().Start(ctx, "load schema")
	defer telemetry.EndWithCause(span, &rerr)

	var schema *introspection.Schema
	var schemaVersion string
	if cfg.IntrospectionJSON != "" {
		var resp introspection.Response
		if err := json.Unmarshal([]byte(cfg.IntrospectionJSON), &resp); err != nil {
			return nil, "", fmt.Errorf("unmarshal introspection json: %w", err)
		}
		schema = resp.Schema
		schemaVersion = resp.SchemaVersion
	} else {
		var err error
		schema, schemaVersion, err = introspection.Introspect(ctx, cfg.Dag)
		if err != nil {
			return nil, "", err
		}
	}

	// Set the parent schema
	generator.SetSchemaParents(schema)
	return schema, schemaVersion, nil
}

func applyGenerated(ctx context.Context, generated *generator.GeneratedState, outputDir string) (rerr error) {
	ctx, span := trace.Tracer().Start(ctx, "write generated files")
	defer telemetry.EndWithCause(span, &rerr)
	return generator.Apply(ctx, generated, outputDir)
}

func runPostCommand(ctx context.Context, cmd *exec.Cmd) (rerr error) {
	_, span := trace.Tracer().Start(ctx, strings.Join(cmd.Args, " "))
	defer telemetry.EndWithCause(span, &rerr)
	return cmd.Run()
}
