package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	gogenerator "github.com/dagger/dagger/cmd/codegen/generator/go"
	typescriptgenerator "github.com/dagger/dagger/cmd/codegen/generator/typescript"
	"github.com/dagger/dagger/cmd/codegen/introspection"
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

	for ctx.Err() == nil {
		generated, err := genFunc(ctx, introspectionSchema, introspectionSchemaVersion)
		if err != nil {
			return err
		}

		if err := generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

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

		if !generated.NeedRegenerate {
			slog.Info("done!")
			break
		}

		slog.Info("needs another pass...")
	}

	return ctx.Err()
}