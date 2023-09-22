package codegen

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/codegen/generator"
	gogenerator "github.com/dagger/dagger/codegen/generator/go"
	nodegenerator "github.com/dagger/dagger/codegen/generator/nodejs"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/engine/client"
	"github.com/moby/buildkit/identity"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

func Generate(
	ctx context.Context,
	cfg generator.Config,
	// TODO: this can probably just be replaced with *dagger.Client now that
	// we're bootstrapped. I'm guessing we're avoiding that because this would
	// otherwise be a circular dependency, since this is used to generate
	// dagger.io/dagger, but we already depend on that through
	// engineClient.Dagger() anyway.
	engineClient *client.Client,
) (rerr error) {
	introspectionSchema, err := generator.Introspect(ctx, engineClient)
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

	vtx := rec.Vertex(digest.FromString(identity.NewID()), vtxName)
	defer func() { vtx.Done(rerr) }()

	for ctx.Err() == nil {
		fmt.Fprintln(vtx.Stderr(), "done!")

		generated, err := generate(ctx, introspectionSchema, cfg)
		if err != nil {
			return err
		}

		if err := generator.Overlay(ctx, generated.Overlay, cfg.OutputDir); err != nil {
			return fmt.Errorf("failed to overlay generated code: %w", err)
		}

		for _, cmd := range generated.PostCommands {
			cmd.Dir = cfg.OutputDir
			cmd.Stdout = vtx.Stdout()
			cmd.Stderr = vtx.Stderr()
			vtx.Task(strings.Join(cmd.Args, " ")).Done(cmd.Run())
		}

		if !generated.NeedRegenerate {
			fmt.Fprintln(vtx.Stderr(), "done!")
			break
		}
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
