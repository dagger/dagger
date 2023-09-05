package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

const (
	modMetaDirPath     = "/.daggermod"
	modMetaInputPath   = "input.json"
	modMetaOutputPath  = "output.json"
	modMetaDepsDirPath = "deps"

	modSourceDirPath      = "/src"
	runtimeExecutablePath = "/runtime"
)

type Module struct {
	// The module's source code root directory
	SourceDirectory *Directory `json:"sourceDirectory"`

	// If set, the subdir of the SourceDirectory that contains the module's source code
	SourceDirectorySubpath string `json:"sourceDirectorySubpath"`

	// The name of the module
	Name string `json:"name"`

	// The SDK of the module
	SDK moduleconfig.SDK `json:"sdk"`

	// Dependencies of the module
	Dependencies []*Module `json:"dependencies"`

	// The module's functions
	Functions []*Function `json:"functions,omitempty"`

	// (Not in public API) The container used to execute the module's entrypoint code,
	// derived from the SDK, source directory, and workdir.
	Runtime *Container `json:"runtime,omitempty"`

	// (Not in public API) The module's platform
	Platform ocispecs.Platform `json:"platform,omitempty"`

	// (Not in public API) The pipeline in which the module was created
	Pipeline pipeline.Path `json:"pipeline,omitempty"`
}

func (mod *Module) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if mod.SourceDirectory != nil {
		dirDefs, err := mod.SourceDirectory.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	if mod.Runtime != nil {
		ctrDefs, err := mod.Runtime.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	for _, dep := range mod.Dependencies {
		depDefs, err := dep.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, depDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() (*Module, error) {
	cp := mod
	if mod.SourceDirectory != nil {
		cp.SourceDirectory = mod.SourceDirectory.Clone()
	}
	if mod.Runtime != nil {
		cp.Runtime = mod.Runtime.Clone()
	}
	cp.Dependencies = make([]*Module, len(mod.Dependencies))
	for i, dep := range mod.Dependencies {
		var err error
		cp.Dependencies[i], err = dep.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone dependency %q: %w", dep.Name, err)
		}
	}
	cp.Functions = make([]*Function, len(mod.Functions))
	for i, function := range mod.Functions {
		var err error
		cp.Functions[i], err = function.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone function %q: %w", function.Name, err)
		}
	}
	return &cp, nil
}

func NewModule(platform ocispecs.Platform, pipeline pipeline.Path) *Module {
	return &Module{
		Platform: platform,
		Pipeline: pipeline,
	}
}

func (mod *Module) FromConfig(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	modCache *ModuleCache,
	installDeps InstallDepsCallback,
	sourceDir *Directory,
	configPath string,
) (*Module, error) {
	configPath = moduleconfig.NormalizeConfigPath(configPath)

	configFile, err := sourceDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}
	configBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg moduleconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	var eg errgroup.Group
	deps := make([]*Module, len(cfg.Dependencies))
	for i, depURL := range cfg.Dependencies {
		i, depURL := i, depURL
		eg.Go(func() error {
			parsedURL, err := moduleconfig.ParseModuleURL(depURL)
			if err != nil {
				return fmt.Errorf("failed to parse dependency url %q: %w", depURL, err)
			}

			// TODO: In theory should first load *just* the config file, figure out the include/exclude, and then load everything else
			// based on that. That's not straightforward because we can't get the config file until we've loaded the dep...
			// May need to have `dagger mod extend` and `dagger mod sync` automatically include dependency include/exclude filters in
			// dagger.json.
			var depSourceDir *Directory
			var depConfigPath string
			switch {
			case parsedURL.Local != nil:
				depSourceDir = sourceDir
				depConfigPath = filepath.Join("/", filepath.Dir(configPath), parsedURL.Local.ConfigPath)
			case parsedURL.Git != nil:
				var err error
				depSourceDir, err = NewDirectorySt(ctx, llb.Git(parsedURL.Git.Repo, parsedURL.Git.Ref), "", mod.Pipeline, mod.Platform, nil)
				if err != nil {
					return fmt.Errorf("failed to create git directory: %w", err)
				}
				depConfigPath = parsedURL.Git.ConfigPath
			default:
				return fmt.Errorf("invalid dependency url from %q", depURL)
			}

			depMod, err := mod.FromConfig(ctx, bk, progSock, modCache, installDeps, depSourceDir, depConfigPath)
			if err != nil {
				return fmt.Errorf("failed to get dependency mod from config %q: %w", depURL, err)
			}
			deps[i] = depMod
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if cfg.Root != "" {
		rootPath := filepath.Join("/", filepath.Dir(configPath), cfg.Root)
		if rootPath != "/" {
			var err error
			sourceDir, err = sourceDir.Directory(ctx, bk, rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root directory: %w", err)
			}
			configPath = filepath.Join("/", strings.TrimPrefix(configPath, rootPath))
		}
	}

	return mod.From(ctx, bk, progSock, modCache, installDeps, cfg.Name, sourceDir, filepath.Dir(configPath), cfg.SDK, deps)
}

func (mod *Module) From(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	modCache *ModuleCache,
	installDeps InstallDepsCallback,
	name string,
	sourceDir *Directory,
	sourceDirSubpath string,
	sdk moduleconfig.SDK,
	deps []*Module,
) (*Module, error) {
	mod = &Module{
		SourceDirectory:        sourceDir,
		SourceDirectorySubpath: sourceDirSubpath,
		Name:                   name,
		SDK:                    sdk,
		Dependencies:           deps,
		Platform:               mod.Platform,
		Pipeline:               mod.Pipeline,
	}

	err := mod.recalcRuntime(ctx, bk, progSock)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container: %w", err)
	}

	resource, _, _, err := mod.execModule(ctx, bk, progSock, modCache, installDeps, "", nil, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to exec module: %w", err)
	}

	var ok bool
	mod, ok = resource.(*Module)
	if !ok {
		return nil, fmt.Errorf("module initialization returned non-module result")
	}

	return mod, mod.updateMod()
}

func (mod *Module) WithFunction(fn *Function, modCache *ModuleCache) (*Module, error) {
	mod, err := mod.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone module: %w", err)
	}
	fn, err = fn.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone function: %w", err)
	}
	mod.Functions = append(mod.Functions, fn)
	return mod, mod.updateMod()
}

// recalculate the definition of the runtime based on the current state of the module
func (mod *Module) recalcRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
) error {
	var runtime *Container
	var err error
	switch mod.SDK {
	case moduleconfig.SDKGo:
		runtime, err = mod.goRuntime(
			ctx,
			bk,
			progSock,
			mod.SourceDirectory,
			mod.SourceDirectorySubpath,
		)
	case moduleconfig.SDKPython:
		runtime, err = mod.pythonRuntime(
			ctx,
			bk,
			progSock,
			mod.SourceDirectory,
			mod.SourceDirectorySubpath,
		)
	default:
		return fmt.Errorf("unknown sdk %q", mod.SDK)
	}
	if err != nil {
		return fmt.Errorf("failed to get base runtime for sdk %s: %w", mod.SDK, err)
	}

	mod.Runtime = runtime
	return mod.updateMod()
}

// TODO: I think now that there's only Function and not a bunch of stuff, you could probably just
// do the callback in the schema package now rather than pass everywhere
type InstallDepsCallback func(context.Context, *Module) error

// TODO: This entire method feels like it might be movable to the schema package now...
func (mod *Module) execModule(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	modCache *ModuleCache,
	installDeps InstallDepsCallback,
	entrypointName string,
	args any,
	cacheExitCode uint32,
) (any, []byte, uint32, error) {
	if err := installDeps(ctx, mod); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to install deps: %w", err)
	}

	ctx, err := modCache.ContextWithCachedMod(ctx, mod)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to set module in context: %w", err)
	}

	argsBytes, err := json.Marshal(args)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal args: %w", err)
	}

	ctr := mod.Runtime

	metaDir := NewScratchDirectory(mod.Pipeline, mod.Platform)
	ctr, err = ctr.WithMountedDirectory(ctx, bk, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	input := FunctionInput{
		Name: entrypointName,
		Args: string(argsBytes),
	}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal input: %w", err)
	}
	inputFileDir, err := NewScratchDirectory(mod.Pipeline, mod.Platform).WithNewFile(ctx, modMetaInputPath, inputBytes, 0600, nil)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create input file: %w", err)
	}
	inputFile, err := inputFileDir.File(ctx, bk, modMetaInputPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to get input file: %w", err)
	}
	ctr, err = ctr.WithMountedFile(ctx, bk, filepath.Join(modMetaDirPath, modMetaInputPath), inputFile, "", true)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to mount input file: %w", err)
	}

	// Mount in read-only dep mod filesystems to ensure that if they change, this mod's cache is
	// also invalidated. Read-only forces buildkit to always use content-based cache keys.
	for _, dep := range mod.Dependencies {
		dirMntPath := filepath.Join(modMetaDirPath, modMetaDepsDirPath, dep.Name, "dir")
		ctr, err = ctr.WithMountedDirectory(ctx, bk, dirMntPath, dep.SourceDirectory, "", true)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to mount dep directory: %w", err)
		}
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, mod.Platform, ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
		CacheExitCode:                 cacheExitCode,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to exec entrypoint: %w", err)
	}
	ctrOutputDir, err := ctr.Directory(ctx, bk, modMetaDirPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to get entrypoint output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx, bk)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to evaluate entrypoint: %w", err)
	}
	if result == nil {
		return nil, nil, 0, fmt.Errorf("entrypoint returned nil result")
	}

	// TODO: if any error happens below, we should really prune the cache of the result, otherwise
	// we can end up in a state where we have a cached result with a dependency blob that we don't
	// guarantee the continued existence of...

	exitCodeStr, err := ctr.MetaFileContents(ctx, bk, progSock, "exitCode")
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to read entrypoint exit code: %w", err)
	}
	exitCodeUint64, err := strconv.ParseUint(exitCodeStr, 10, 32)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to parse entrypoint exit code: %w", err)
	}
	exitCode := uint32(exitCodeUint64)

	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: modMetaOutputPath,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to read entrypoint output file: %w", err)
	}

	var rawOutput any
	if err := json.Unmarshal(outputBytes, &rawOutput); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	strOutput, ok := rawOutput.(string)
	if !ok {
		// not a resource ID, nothing to do
		return nil, outputBytes, exitCode, nil
	}

	resource, err := ResourceFromID(strOutput)
	if err != nil {
		// not a resource ID, nothing to do
		// TODO: check actual error type
		return nil, outputBytes, exitCode, nil
	}

	pbDefinitioner, ok := resource.(HasPBDefinitions)
	if !ok {
		// no dependency blobs to handle
		return resource, outputBytes, exitCode, nil
	}

	pbDefs, err := pbDefinitioner.PBDefinitions()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to get pb definitions: %w", err)
	}
	dependencyBlobs := map[digest.Digest]*ocispecs.Descriptor{}
	for _, pbDef := range pbDefs {
		dag, err := buildkit.DefToDAG(pbDef)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to convert pb definition to dag: %w", err)
		}
		blobs, err := dag.BlobDependencies()
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to get blob dependencies: %w", err)
		}
		for k, v := range blobs {
			dependencyBlobs[k] = v
		}
	}

	if err := result.Ref.AddDependencyBlobs(ctx, dependencyBlobs); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to add dependency blob: %w", err)
	}

	return resource, outputBytes, exitCode, nil
}

// TODO: doc various subtleties below

// update existing entrypoints with the current state of their module
func (mod *Module) updateMod() error {
	modID, err := mod.ID()
	if err != nil {
		return fmt.Errorf("failed to get module ID: %w", err)
	}
	if err := mod.setFunctionMods(modID); err != nil {
		return fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return nil
}

func (id ModuleID) Decode() (*Module, error) {
	mod, err := resourceid.ID[Module](id).Decode()
	if err != nil {
		return nil, err
	}
	if err := mod.setFunctionMods(id); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return mod, nil
}

func (mod *Module) ID() (ModuleID, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	if err := mod.setFunctionMods(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	id, err := resourceid.Encode(mod)
	if err != nil {
		return "", fmt.Errorf("failed to encode module to id: %w", err)
	}
	return ModuleID(id), nil
}

func (mod *Module) Digest() (digest.Digest, error) {
	mod, err := mod.Clone()
	if err != nil {
		return "", fmt.Errorf("failed to clone module: %w", err)
	}
	if err := mod.setFunctionMods(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint mods: %w", err)
	}
	return stableDigest(mod)
}

func (mod *Module) setFunctionMods(id ModuleID) error {
	// TODO: recurse to functions of other objects in the module
	for _, fn := range mod.Functions {
		if fn.ModuleID == "" {
			fn.ModuleID = id
			continue
		}
		fnMod, err := fn.ModuleID.Decode()
		if err != nil {
			return fmt.Errorf("failed to decode module for function %q: %w", fn.Name, err)
		}
		if fnMod.Name == mod.Name {
			fn.ModuleID = id
		}
	}
	return nil
}

type ModuleCache CacheMap[digest.Digest, *Module]

func NewModuleCache() *ModuleCache {
	return (*ModuleCache)(NewCacheMap[digest.Digest, *Module]())
}

func (cache *ModuleCache) cacheMap() *CacheMap[digest.Digest, *Module] {
	return (*CacheMap[digest.Digest, *Module])(cache)
}

func (cache *ModuleCache) ContextWithCachedMod(ctx context.Context, mod *Module) (context.Context, error) {
	modDigest, err := mod.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	clientMetadata.ModuleDigest = modDigest

	_, err = cache.cacheMap().GetOrInitialize(modDigest, func() (*Module, error) {
		return mod, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to cache module: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}

func (cache *ModuleCache) CachedModFromContext(ctx context.Context) (*Module, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	return cache.cacheMap().GetOrInitialize(clientMetadata.ModuleDigest, func() (*Module, error) {
		return nil, fmt.Errorf("module %s not found in cache", clientMetadata.ModuleDigest)
	})
}
