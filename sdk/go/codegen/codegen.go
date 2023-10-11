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
	"dagger.io/dagger/modules"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

func Generate(ctx context.Context, cfg generator.Config, dag *dagger.Client) (rerr error) {
	rec := progrock.FromContext(ctx)

	var vtxName string
	if cfg.ModuleConfig != nil {
		vtxName = fmt.Sprintf("generating %s module: %s", cfg.Lang, cfg.ModuleConfig.Name)
	} else {
		vtxName = fmt.Sprintf("generating %s SDK client", cfg.Lang)
	}

	vtx := rec.Vertex(digest.FromString(time.Now().String()), vtxName)
	defer func() { vtx.Done(rerr) }()

	logsW := vtx.Stdout()

	if cfg.ModuleConfig != nil {
		// TODO: this works but isn't perfect.
		//
		// We want to introspect the module's 'schema view', but we're actually
		// running from the SDK module, so this will also pick up any of the SDK
		// module's dependencies. Thankfully there aren't any at the moment.
		//
		// In fact, this only matters for a manual `dagger mod sync'. For on-the-fly
		// codegen, the `codegen` command will actually be running from within the
		// module's schema view, so it will see the following code isn't needed. But
		// even in that case it might be better to be more explicit rather than build
		// up a Container that only "works" when it's run the right way.

		ref := cfg.ModuleRef

		loadDeps := new(errgroup.Group)

		for _, dep := range cfg.ModuleConfig.Dependencies {
			dep := dep
			loadDeps.Go(func() error {
				depRef, err := modules.ResolveModuleDependency(ctx, dag, ref, dep)
				if err != nil {
					return fmt.Errorf("resolve module dependency %q: %w", dep, err)
				}
				depMod, err := depRef.AsModule(ctx, dag)
				if err != nil {
					return fmt.Errorf("resolve module dependency %q: %w", dep, err)
				}
				_, err = depMod.Serve(ctx)
				if err != nil {
					return fmt.Errorf("serve module dependency %q: %w", dep, err)
				}
				return err
			})
		}

		if err := loadDeps.Wait(); err != nil {
			return err
		}
	}

	introspectionSchema, err := generator.Introspect(ctx, dag)
	if err != nil {
		return err
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
