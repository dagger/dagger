package main

import (
	"context"
	"fmt"
	"path/filepath"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
	"typescript-sdk/tsutils"

	"golang.org/x/sync/errgroup"
)

type DenoRuntime struct {
	sdkSourceDir      *dagger.Directory
	introspectionJSON *dagger.File
	cfg               *moduleConfig
	ctr               *dagger.Container
}

func NewDenoRuntime(
	cfg *moduleConfig,
	sdkSourceDir *dagger.Directory,
	introspectionJSON *dagger.File,
) *DenoRuntime {
	cacheVolumeName := fmt.Sprintf("mod-bun-cache-%s", tsdistconsts.DefaultBunVersion)

	ctr := dag.Container().
		From(cfg.image).
		WithMountedCache("/root/.deno/cache", dag.CacheVolume(cacheVolumeName)).
		WithWorkdir(cfg.modulePath())

	return &DenoRuntime{
		sdkSourceDir:      sdkSourceDir,
		introspectionJSON: introspectionJSON,
		cfg:               cfg,
		ctr:               ctr,
	}
}

func (d *DenoRuntime) SetupContainer(ctx context.Context) (*dagger.Container, error) {
	var sdkLibrary *dagger.Directory
	var denoRuntimeWithDep *DenoRuntime

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "generate SDK library")
		defer span.End()

		sdkLibrary, err = NewLibGenerator(d.sdkSourceDir).
			GenerateBundleLibrary(d.introspectionJSON, d.cfg.name, d.cfg.modulePath()).
			Sync(ctx)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "setup deno container with installed dependencies")
		defer span.End()

		denoRuntimeWithDep, err = d.withDenoJSON(ctx)
		if err != nil {
			return err
		}

		denoRuntimeWithDep, err = denoRuntimeWithDep.withInstalledDependencies().sync(ctx)
		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	entrypointPath := filepath.Join(d.cfg.modulePath(), SrcDir, EntrypointExecutableFile)

	return denoRuntimeWithDep.ctr.
		WithMountedDirectory(GenDir, sdkLibrary).
		WithMountedDirectory("src", d.cfg.source.Directory("src")).
		WithMountedFile(entrypointPath, entrypointFile()).
		WithEntrypoint([]string{
			"deno", "run", "-q", "-A", entrypointPath,
		}), nil
}

// We do not generate a `deno.lock` file because it requires to specify
// a source file to generate the lock file from. We don't want that
// because it would invalidate the cache on each code change.
func (d *DenoRuntime) GenerateDir(ctx context.Context) (*dagger.Directory, error) {
	var denoJSON *dagger.File
	var sdkLibrary *dagger.Directory

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "generate SDK library")
		defer span.End()

		sdkLibrary, err = NewLibGenerator(d.sdkSourceDir).
			GenerateBundleLibrary(d.introspectionJSON, d.cfg.name, d.cfg.modulePath()).
			Sync(ctx)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "update deno.json")
		defer span.End()

		runtime, err := d.withDenoJSON(ctx)
		if err != nil {
			return err
		}

		denoJSON = runtime.ctr.File("deno.json")
		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// Merge all generated/updated files into a single directory.
	return dag.Directory().
		WithFile("deno.json", denoJSON).
		WithDirectory(GenDir, sdkLibrary), nil
}

func (d *DenoRuntime) withDenoJSON(ctx context.Context) (*DenoRuntime, error) {
	denoFileContent, err := d.cfg.source.File("deno.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read deno.json: %w", err)
	}

	// Update deno.json
	denoFileContent, err = tsutils.UpdateDenoConfigForModule(denoFileContent)
	if err != nil {
		return nil, fmt.Errorf("failed to update deno.json: %w", err)
	}

	d.ctr = d.ctr.WithNewFile("deno.json", denoFileContent)

	return d, nil
}

func (d *DenoRuntime) sync(ctx context.Context) (*DenoRuntime, error) {
	ctr, err := d.ctr.Sync(ctx)
	if err != nil {
		return nil, err
	}

	d.ctr = ctr

	return d, nil
}

func (d *DenoRuntime) withInstalledDependencies() *DenoRuntime {
	version, ok := d.cfg.denoJSONConfig.Imports["typescript"]
	if ok && version == "npm:typescript@"+tsdistconsts.DefaultTypeScriptVersion {
		// Pre-warm cache for the typescript library if the default version is used.
		d.ctr = d.ctr.
			WithMountedDirectory("/deno-dir/npm/registry.npmjs.org/typescript/5.9.3", d.sdkSourceDir.Directory("typescript-library"))
	}

	d.ctr = d.ctr.
		// We mount the host `deno.json` because the updated one is
		// stateless and shouldn't change the dependency installed.
		WithExec([]string{"deno", "install", "--node-modules-dir=auto"})

	return d
}
