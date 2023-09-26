package codegen

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen/generator"
	gogenerator "dagger.io/dagger/codegen/generator/go"
	nodegenerator "dagger.io/dagger/codegen/generator/nodejs"
	"dagger.io/dagger/codegen/introspection"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

func Generate(ctx context.Context, cfg generator.Config, dag *dagger.Client) (rerr error) {
	introspectionSchema, err := generator.Introspect(ctx, dag)
	if err != nil {
		return err
	}

	rec := progrock.FromContext(ctx)

	var vtxName string
	if cfg.ModuleName != "" {
		vtxName = fmt.Sprintf("generating %s module: %s", cfg.Lang, cfg.ModuleName)
	} else {
		vtxName = fmt.Sprintf("generating %s SDK client", cfg.Lang)
	}

	vtx := rec.Vertex(digest.FromString(time.Now().String()), vtxName)
	defer func() { vtx.Done(rerr) }()

	logsW := vtx.Stdout()

	for ctx.Err() == nil {
		generated, err := generate(ctx, introspectionSchema, cfg)
		if err != nil {
			return err
		}

		if err := generator.Overlay(ctx, logsW, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		for _, cmd := range generated.PostCommands {
			cmd.Dir = cfg.OutputDir
			cmd.Stdout = vtx.Stdout()
			cmd.Stderr = vtx.Stderr()
			vtx.Task(strings.Join(cmd.Args, " ")).Done(cmd.Run())
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
	case generator.SDKLangNodeJS:
		gen = &nodegenerator.NodeGenerator{
			Config: cfg,
		}

	default:
		sdks := []string{
			string(generator.SDKLangGo),
			string(generator.SDKLangNodeJS),
		}
		return nil, fmt.Errorf("use target SDK language: %s: %w", sdks, generator.ErrUnknownSDKLang)
	}

	return gen.Generate(ctx, introspectionSchema)
}
