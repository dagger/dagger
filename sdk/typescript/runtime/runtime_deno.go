package main

import (
	"context"
	"fmt"
	"path/filepath"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

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
		WithMountedCache("/root/.deno/cache", dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeShared,
		}).
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

		sdkLibrary, err = NewLibGenerator(d.sdkSourceDir, d.cfg.libGeneratorOpts()).
			GenerateBundleLibrary(d.introspectionJSON, ModSourceDirPath).
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

	ctr := denoRuntimeWithDep.ctr.
		WithMountedDirectory(GenDir, sdkLibrary).
		// Make @dagger.io/dagger resolvable for ts-introspector (it doesn't read tsconfig paths).
		WithMountedDirectory("node_modules/@dagger.io/dagger", sdkLibrary).
		// Merge source code directory with current directory
		WithDirectory(".", d.cfg.wrappedSourceCodeDirectory()).
		WithMountedFile(entrypointPath, entrypointFile()).
		WithEntrypoint([]string{
			"deno", "run", "-q", "-A", entrypointPath,
		})

	if d.cfg.debug {
		ctr = ctr.Terminal()
	}

	return ctr, nil
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

		sdkLibrary, err = NewLibGenerator(d.sdkSourceDir, d.cfg.libGeneratorOpts()).
			GenerateBundleLibrary(d.introspectionJSON, ModSourceDirPath).
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
		WithDirectory(GenDir, sdkLibrary).
		// Also add the source directory so it's accessible from `dag.currentModule().source()`
		WithDirectory(".", d.cfg.wrappedSourceCodeDirectory()), nil
}

func (d *DenoRuntime) withDenoJSON(ctx context.Context) (*DenoRuntime, error) {
	denoJSONFile, err := UpdateDenoJSONForModule(ctx, d.cfg.source.File("deno.json"))
	if err != nil {
		return nil, err
	}

	d.ctr = d.ctr.WithFile("deno.json", denoJSONFile)

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
