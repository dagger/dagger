package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	gogenerator "github.com/dagger/dagger/cmd/codegen/generator/go"
	typescriptgenerator "github.com/dagger/dagger/cmd/codegen/generator/typescript"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/koron-go/prefixw"
)

func Generate(ctx context.Context, cfg generator.Config, dag *dagger.Client) (err error) {
	logsW := os.Stdout

	if cfg.ModuleName != "" {
		fmt.Fprintf(logsW, "generating %s module: %s\n", cfg.Lang, cfg.ModuleName)
	} else {
		fmt.Fprintf(logsW, "generating %s SDK client\n", cfg.Lang)
	}

	var introspectionSchema *introspection.Schema
	if cfg.IntrospectionJSON != "" {
		var resp introspection.Response
		if err := json.Unmarshal([]byte(cfg.IntrospectionJSON), &resp); err != nil {
			return fmt.Errorf("unmarshal introspection json: %w", err)
		}
		introspectionSchema = resp.Schema
	} else {
		introspectionSchema, err = generator.Introspect(ctx, dag)
		if err != nil {
			return err
		}
	}

	for ctx.Err() == nil {
		generated, err := generate(ctx, introspectionSchema, cfg)
		if err != nil {
			return err
		}

		if err := generator.Overlay(ctx, logsW, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		for _, cmd := range generated.PostCommands {
			pw := prefixw.New(logsW, strings.Join(cmd.Args, " ")+" | ")
			cmd.Dir = cfg.OutputDir
			cmd.Stdout = pw
			cmd.Stderr = pw
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(pw, "error: %s\n", err)
				return fmt.Errorf("failed to run post command: %w", err)
			}
		}

		if !generated.NeedRegenerate {
			fmt.Fprintln(logsW, "done!")
			break
		}

		fmt.Fprintln(logsW, "needs another pass...")
	}

	return ctx.Err()
}

func generate(ctx context.Context, introspectionSchema *introspection.Schema, cfg generator.Config) (*generator.GeneratedState, error) {
	generator.SetSchemaParents(introspectionSchema)

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
			string(generator.SDKLangTypeScript),
		}
		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
