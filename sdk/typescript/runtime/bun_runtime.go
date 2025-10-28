package main

import (
	"context"
	"fmt"
	"path/filepath"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

	"golang.org/x/sync/errgroup"
)

type BunRuntime struct {
	SDKSourceDir      *dagger.Directory
	introspectionJSON *dagger.File
	cfg               *moduleConfig
	ctr               *dagger.Container
}

func NewBunRuntime(
	cfg *moduleConfig,
	sdkSourceDir *dagger.Directory,
	introspectionJSON *dagger.File,
) *BunRuntime {
	// Q: Should the cacheVolumeName depends on the cfg.image version
	cacheVolumeName := fmt.Sprintf("mod-bun-cache-%s", tsdistconsts.DefaultBunVersion)

	ctr := dag.Container().
		From(cfg.image).
		WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModePrivate,
		}).
		WithWorkdir(cfg.modulePath())

	return &BunRuntime{
		SDKSourceDir:      sdkSourceDir,
		introspectionJSON: introspectionJSON,
		cfg:               cfg,
		ctr:               ctr,
	}
}

func (b *BunRuntime) Setup(ctx context.Context) (*dagger.Container, error) {
	var tsConfig *dagger.File
	var sdkLibrary *dagger.Directory
	var runtimeWithDep *BunRuntime

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "update tsconfig.json")
		defer span.End()
		tsConfig, err = CreateOrUpdateTSConfig(ctx, b.cfg.source)

		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "generate SDK library")
		defer span.End()

		sdkLibrary, err = NewLibGenerator(b.SDKSourceDir).
			GenerateBundleLibrary(b.introspectionJSON, b.cfg.name, b.cfg.modulePath()).
			Sync(ctx)

		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "install dependencies")
		defer span.End()

		runtimeWithDep, err = b.
			WithPackageJSON().
			withInstalledDependencies(b.SDKSourceDir.Directory("typescript-library")).
			sync(ctx)

		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return runtimeWithDep.
		withSDKLibrary(sdkLibrary).
		withTsConfig(tsConfig).
		withModuleCode().
		asEntrypoint(), nil
}

func (b *BunRuntime) sync(ctx context.Context) (*BunRuntime, error) {
	ctr, err := b.ctr.Sync(ctx)
	if err != nil {
		return nil, err
	}

	b.ctr = ctr

	return b, nil
}

func (b *BunRuntime) WithPackageJSON() *BunRuntime {
	var packageJSONFile *dagger.File

	if b.cfg.packageJSONConfig != nil {
		packageJSONFile = b.cfg.source.File("package.json")
	} else {
		packageJSONFile = defaultPackageJSONFile()
	}

	ctr := b.ctr.
		WithFile("package.json", packageJSONFile)

	// Set the type to module
	ctr = ctr.WithExec([]string{"bun", "pm", "pkg", "set", "type=module"})

	// Set the typescript dependency if it's not already set
	_, ok := b.cfg.packageJSONConfig.Dependencies["typescript"]
	if !ok {
		ctr = ctr.WithExec([]string{"bun", "pm", "pkg", "set", fmt.Sprintf("dependencies.typescript=%s", tsdistconsts.DefaultTypeScriptVersion)})
	}

	// If the package.json has a `@dagger.io/dagger` as dependency, we remove it to automatically
	// switch to bundle.
	_, ok = b.cfg.packageJSONConfig.Dependencies["@dagger.io/dagger"]
	if ok {
		ctr = ctr.WithExec([]string{"bun", "pm", "pkg", "delete", "dependencies.@dagger.io/dagger"})
	}

	b.ctr = ctr

	return b
}

func (b *BunRuntime) GetLockfile() *dagger.File {
	return b.ctr.
		WithDirectory(".", b.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{"bun.lock"},
		}).
		WithExec([]string{"bun", "install", "--lockfile-only"}).
		File("/work/bun.lock")
}

func (b *BunRuntime) withInstalledDependencies(typescriptLib *dagger.Directory) *BunRuntime {
	version, ok := b.cfg.packageJSONConfig.Dependencies["typescript"]
	if ok && version == tsdistconsts.DefaultTypeScriptVersion {
		b.ctr = b.ctr.
			WithMountedDirectory("node_modules/typescript", typescriptLib)

		// If there's only the default typescript dependency, we can early return.
		if len(b.cfg.packageJSONConfig.Dependencies) == 1 {
			return b
		}
	}

	b.ctr = b.ctr.
		WithExec([]string{"bun", "install", "--no-verify", "--omit=dev", "--omit=peer", "--omit=optional"})

	return b
}

func (b *BunRuntime) withSDKLibrary(sdkLibrary *dagger.Directory) *BunRuntime {
	b.ctr = b.ctr.
		WithMountedDirectory(
			GenDir,
			sdkLibrary,
		)

	return b
}

func (b *BunRuntime) withModuleCode() *BunRuntime {
	b.ctr = b.ctr.
		WithMountedDirectory(
			"src",
			b.cfg.source.Directory("src"),
		)

	return b
}

func (b *BunRuntime) withTsConfig(tsconfigFile *dagger.File) *BunRuntime {
	b.ctr = b.ctr.
		WithMountedFile("tsconfig.json", tsconfigFile)

	return b
}

func (b *BunRuntime) asEntrypoint() *dagger.Container {
	entrypointPath := filepath.Join(b.cfg.modulePath(), SrcDir, EntrypointExecutableFile)

	return b.ctr.
		WithMountedFile(entrypointPath, entrypointFile()).
		WithEntrypoint([]string{
			"bun", "run", entrypointPath,
		})
}
