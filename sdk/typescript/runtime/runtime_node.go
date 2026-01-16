package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

	"golang.org/x/sync/errgroup"
)

type NodeRuntime struct {
	sdkSourceDir      *dagger.Directory
	introspectionJSON *dagger.File
	cfg               *moduleConfig
	ctr               *dagger.Container
}

func NewNodeRuntime(
	cfg *moduleConfig,
	sdkSourceDir *dagger.Directory,
	introspectionJSON *dagger.File,
) *NodeRuntime {
	ctr := dag.
		Container().
		From(cfg.image).
		WithWorkdir(cfg.modulePath()).
		// Install default CA certificates and configure node to use them instead of its compiled in CA bundle.
		// This enables use of custom CA certificates if configured in the dagger engine.
		WithExec([]string{"apk", "add", "ca-certificates"}).
		WithEnvVariable("NODE_OPTIONS", "--use-openssl-ca").
		// install tsx from its bundled location in the engine image
		WithMountedDirectory("/usr/local/lib/node_modules/tsx", sdkSourceDir.Directory("/tsx_module")).
		WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"})

	return &NodeRuntime{
		sdkSourceDir:      sdkSourceDir,
		introspectionJSON: introspectionJSON,
		cfg:               cfg,
		ctr:               ctr,
	}
}

func (n *NodeRuntime) SetupContainer(ctx context.Context) (*dagger.Container, error) {
	var tsConfig *dagger.File
	var sdkLibrary *dagger.Directory
	var runtimeWithDep *NodeRuntime

	eg, gctx := errgroup.WithContext(ctx)

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(gctx, "update tsconfig.json")
		defer span.End()

		tsConfig, err = CreateOrUpdateTSConfigForModule(ctx, n.cfg.source)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(gctx, "generate SDK library")
		defer span.End()

		sdkLibrary, err = NewLibGenerator(n.sdkSourceDir, n.cfg.libGeneratorOpts()).
			GenerateBundleLibrary(n.introspectionJSON, ModSourceDirPath).
			Sync(ctx)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(gctx, "install dependencies")
		defer span.End()

		runtimeWithPkgJSON, err := n.withPackageJSON(ctx)
		if err != nil {
			return err
		}

		pkgManager := runtimeWithPkgJSON.createPkgManagerCtr()

		runtimeWithDep, err = pkgManager.
			withSetupPackageManager().
			withInstalledDependencies().
			sync(ctx)

		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	entrypointPath := filepath.Join(n.cfg.modulePath(), SrcDir, EntrypointExecutableFile)

	// Merge all the generated files together and setup an entrypoint command.
	ctr := runtimeWithDep.ctr.
		WithMountedDirectory(GenDir, sdkLibrary).
		// Make @dagger.io/dagger resolvable for ts-introspector (it doesn't read tsconfig paths).
		WithMountedDirectory("node_modules/@dagger.io/dagger", sdkLibrary).
		WithMountedFile("tsconfig.json", tsConfig).
		// Merge source code directory with current directory
		WithDirectory(".", n.cfg.wrappedSourceCodeDirectory()).
		WithMountedFile(entrypointPath, entrypointFile()).
		WithEntrypoint([]string{
			"tsx", "--no-deprecation", "--tsconfig", n.cfg.tsConfigPath(), entrypointPath,
		})

	if n.cfg.debug {
		ctr = ctr.Terminal()
	}

	return ctr, nil
}

func (n *NodeRuntime) GenerateDir(ctx context.Context) (*dagger.Directory, error) {
	var tsconfigFile *dagger.File
	var sdkLibrary *dagger.Directory
	var lockFile *dagger.File

	runtime, err := n.withPackageJSON(ctx)
	if err != nil {
		return nil, err
	}

	pkgManager := runtime.createPkgManagerCtr()

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "update tsconfig.json")
		defer span.End()

		tsconfigFile, err = CreateOrUpdateTSConfigForModule(ctx, n.cfg.source)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "generate SDK library")
		defer span.End()

		sdkLibrary, err = NewLibGenerator(n.sdkSourceDir, n.cfg.libGeneratorOpts()).
			GenerateBundleLibrary(n.introspectionJSON, ModSourceDirPath).
			Sync(ctx)
		return err
	})

	eg.Go(func() (err error) {
		ctx, span := Tracer().Start(ctx, "generate lock file")
		defer span.End()

		lockFile, err = pkgManager.
			withSetupPackageManager().
			generateLockFile().
			Sync(ctx)

		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return dag.Directory().
		WithFile("package.json", runtime.ctr.File("package.json")).
		WithFile("tsconfig.json", tsconfigFile).
		WithFile(pkgManager.lockFileName(), lockFile).
		WithDirectory(GenDir, sdkLibrary).
		// Also add the source directory so it's accessible from `dag.currentModule().source()`
		WithDirectory(".", n.cfg.wrappedSourceCodeDirectory()), nil
}

func (n *NodeRuntime) createPkgManagerCtr() PackageManager {
	switch n.cfg.packageManager {
	case Yarn:
		return NewYarnPkgManager(n)
	case Pnpm:
		return NewPnpmPkgManager(n)
	case Npm:
		return NewNpmPkgManager(n)
	default:
		// Impossible to fallback here since we already checked the package manager
		// in the config.
		return nil
	}
}

func (n *NodeRuntime) sync(ctx context.Context) (*NodeRuntime, error) {
	ctr, err := n.ctr.Sync(ctx)
	if err != nil {
		return nil, err
	}

	n.ctr = ctr

	return n, nil
}

func (n *NodeRuntime) withPackageJSON(ctx context.Context) (*NodeRuntime, error) {
	var packageJSONFile *dagger.File

	if n.cfg.packageJSONConfig != nil {
		packageJSONFile = n.cfg.source.File("package.json")
	} else {
		packageJSONFile = defaultPackageJSONFile()

		n.cfg.packageJSONConfig = &packageJSONConfig{
			Dependencies: make(map[string]string),
		}
	}

	packageJSONFile, err := CreateOrUpdatePackageJSONForModule(ctx, packageJSONFile)
	if err != nil {
		return nil, fmt.Errorf("failed to update package.json: %w", err)
	}

	n.ctr = n.ctr.
		WithFile("package.json", packageJSONFile)

	return n, nil
}

type PackageManager interface {
	withSetupPackageManager() PackageManager
	withInstalledDependencies() *NodeRuntime
	generateLockFile() *dagger.File
	lockFileName() string
}

type YarnPkgManager struct {
	*NodeRuntime
}

func NewYarnPkgManager(runtime *NodeRuntime) *YarnPkgManager {
	cacheVolumeName := fmt.Sprintf("yarn-cache-%s-%s", runtime.cfg.runtime, runtime.cfg.runtimeVersion)

	runtime.ctr = runtime.ctr.
		WithMountedCache("/root/.cache/yarn", dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeShared,
		})

	return &YarnPkgManager{
		NodeRuntime: runtime,
	}
}

// We do not install a specific yarn version because corepack will
// automatically install the dependencies from the package.json file.
func (y *YarnPkgManager) withSetupPackageManager() PackageManager {
	return y
}

func (y *YarnPkgManager) withInstalledDependencies() *NodeRuntime {
	version, ok := y.cfg.packageJSONConfig.Dependencies["typescript"]
	if ok && version == tsdistconsts.DefaultTypeScriptVersion {
		y.NodeRuntime.ctr = y.NodeRuntime.ctr.
			WithMountedDirectory("node_modules/typescript", y.sdkSourceDir.Directory("typescript-library"))

		// If there's only the default typescript dependency, we can early return.
		if len(y.cfg.packageJSONConfig.Dependencies) <= 1 {
			return y.NodeRuntime
		}
	}
	y.NodeRuntime.ctr = y.NodeRuntime.ctr.
		WithDirectory(".", y.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{y.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"yarn", "install", "--prod"})

	return y.NodeRuntime
}

func (y *YarnPkgManager) generateLockFile() *dagger.File {
	return y.NodeRuntime.ctr.
		WithDirectory(".", y.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{y.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"yarn", "install", "--mode", "update-lockfile"}).
		File(y.lockFileName())
}

func (y *YarnPkgManager) lockFileName() string {
	return "yarn.lock"
}

type NpmPkgManager struct {
	*NodeRuntime
}

func NewNpmPkgManager(runtime *NodeRuntime) *NpmPkgManager {
	cacheVolumeName := fmt.Sprintf("npm-cache-%s-%s", runtime.cfg.runtime, runtime.cfg.runtimeVersion)

	runtime.ctr = runtime.ctr.
		WithMountedCache("/root/.npm", dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeShared,
		})

	return &NpmPkgManager{
		NodeRuntime: runtime,
	}
}

func (n *NpmPkgManager) withSetupPackageManager() PackageManager {
	version := n.cfg.packageManagerVersion

	n.NodeRuntime.ctr = n.NodeRuntime.ctr.
		WithExec([]string{"sh", "-c", "npm -v | grep -qx " + strings.ReplaceAll(version, ".", `\.`) + " || npm install -g npm@" + version})

	return n
}

func (n *NpmPkgManager) withInstalledDependencies() *NodeRuntime {
	version, ok := n.cfg.packageJSONConfig.Dependencies["typescript"]
	if ok && version == tsdistconsts.DefaultTypeScriptVersion {
		n.NodeRuntime.ctr = n.NodeRuntime.ctr.
			WithMountedDirectory("node_modules/typescript", n.sdkSourceDir.Directory("typescript-library"))

		// If there's only the default typescript dependency, we can early return.
		if len(n.cfg.packageJSONConfig.Dependencies) <= 1 {
			return n.NodeRuntime
		}
	}

	// We need to install the dependencies with the package manager
	n.NodeRuntime.ctr = n.NodeRuntime.ctr.
		WithDirectory(".", n.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{n.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"npm", "install", "--omit=dev"})

	return n.NodeRuntime
}

func (n *NpmPkgManager) generateLockFile() *dagger.File {
	return n.NodeRuntime.ctr.
		WithDirectory(".", n.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{n.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"npm", "install", "--package-lock-only"}).
		File(n.lockFileName())
}

func (n *NpmPkgManager) lockFileName() string {
	return "package-lock.json"
}

type PnpmPkgManager struct {
	*NodeRuntime
}

func NewPnpmPkgManager(runtime *NodeRuntime) *PnpmPkgManager {
	cacheVolumeName := fmt.Sprintf("pnpm-cache-%s-%s", runtime.cfg.runtime, runtime.cfg.runtimeVersion)

	runtime.ctr = runtime.ctr.
		WithMountedCache("/root/.pnpm-store", dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeShared,
		})

	return &PnpmPkgManager{
		NodeRuntime: runtime,
	}
}

func (p *PnpmPkgManager) withSetupPackageManager() PackageManager {
	p.NodeRuntime.ctr = p.NodeRuntime.ctr.WithExec([]string{
		"npm", "install", "-g",
		fmt.Sprintf("pnpm@%s", p.cfg.packageManagerVersion)})

	return p
}

func (p *PnpmPkgManager) withInstalledDependencies() *NodeRuntime {
	version, ok := p.cfg.packageJSONConfig.Dependencies["typescript"]
	if ok && version == tsdistconsts.DefaultTypeScriptVersion {
		p.NodeRuntime.ctr = p.NodeRuntime.ctr.
			WithMountedDirectory("node_modules/typescript", p.sdkSourceDir.Directory("typescript-library"))

		// If there's only the default typescript dependency, we can early return.
		if len(p.cfg.packageJSONConfig.Dependencies) <= 1 {
			return p.NodeRuntime
		}
	}

	p.NodeRuntime.ctr = p.NodeRuntime.ctr.
		WithDirectory(".", p.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{p.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"pnpm", "install", "--shamefully-hoist=true", "--prod"})

	return p.NodeRuntime
}

func (p *PnpmPkgManager) generateLockFile() *dagger.File {
	return p.NodeRuntime.ctr.
		WithDirectory(".", p.cfg.source, dagger.ContainerWithDirectoryOpts{
			Include: []string{p.lockFileName(), ".npmrc"},
		}).
		WithExec([]string{"pnpm", "install", "--lockfile-only"}).
		File(p.lockFileName())
}

func (p *PnpmPkgManager) lockFileName() string {
	return "pnpm-lock.yaml"
}
