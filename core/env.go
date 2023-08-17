package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core/envconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

const (
	envMetaDirPath     = "/.daggerenv"
	envMetaInputPath   = "input.json"
	envMetaOutputPath  = "output.json"
	envMetaDepsDirPath = "deps"

	envSourceDirPath      = "/src"
	envWorkdirPath        = "/wd"
	runtimeExecutablePath = "/runtime"
)

type Environment struct {
	// The environment's source code root directory
	SourceDirectory *Directory `json:"sourceDirectory"`

	// If set, the subdir of the SourceDirectory that contains the environment's source code
	SourceDirectorySubpath string `json:"sourceDirectorySubpath"`

	// The directory in which environment code executes as its current working directory
	Workdir *Directory `json:"workdir"`

	// The name of the environment
	Name string `json:"name"`

	// The SDK of the environment
	SDK envconfig.SDK `json:"sdk"`

	// Dependencies of the environment
	Dependencies []*Environment `json:"dependencies"`

	// The environment's checks
	Checks []*Check `json:"checks,omitempty"`

	// (Not in public API) The container used to execute the environment's entrypoint code,
	// derived from the SDK, source directory, and workdir.
	Runtime *Container `json:"runtime,omitempty"`

	// (Not in public API) The environment's platform
	Platform specs.Platform `json:"platform,omitempty"`

	// (Not in public API) The pipeline in which the environment was created
	Pipeline pipeline.Path `json:"pipeline,omitempty"`
}

func (env *Environment) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if env.SourceDirectory != nil {
		dirDefs, err := env.SourceDirectory.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	if env.Workdir != nil && env.Workdir != env.SourceDirectory {
		workdirDefs, err := env.Workdir.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, workdirDefs...)
	}
	if env.Runtime != nil {
		ctrDefs, err := env.Runtime.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	for _, dep := range env.Dependencies {
		depDefs, err := dep.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, depDefs...)
	}
	for _, check := range env.Checks {
		checkDefs, err := check.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, checkDefs...)
	}
	return defs, nil
}

func (env Environment) Clone() *Environment {
	cp := env
	if env.SourceDirectory != nil {
		cp.SourceDirectory = env.SourceDirectory.Clone()
	}
	if env.Workdir != nil {
		cp.Workdir = env.Workdir.Clone()
	}
	if env.Runtime != nil {
		cp.Runtime = env.Runtime.Clone()
	}
	cp.Dependencies = make([]*Environment, len(env.Dependencies))
	for i, dep := range env.Dependencies {
		cp.Dependencies[i] = dep.Clone()
	}
	cp.Checks = make([]*Check, len(env.Checks))
	for i, check := range env.Checks {
		cp.Checks[i] = check.Clone()
	}
	return &cp
}

func NewEnvironment(platform specs.Platform, pipeline pipeline.Path) *Environment {
	return &Environment{
		Platform: platform,
		Pipeline: pipeline,
	}
}

func (env *Environment) From(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	envCache *EnvironmentCache,
	installDeps InstallDepsCallback,
	name string,
	sourceDir *Directory,
	sourceDirSubpath string,
	sdk envconfig.SDK,
	deps []*Environment,
) (*Environment, error) {
	env = &Environment{
		SourceDirectory:        sourceDir,
		SourceDirectorySubpath: sourceDirSubpath,
		Workdir:                sourceDir, // defaults to root of source dir, can be changed via WithWorkdir
		Name:                   name,
		SDK:                    sdk,
		Dependencies:           deps,
		Platform:               env.Platform,
		Pipeline:               env.Pipeline,
	}

	err := env.recalcRuntime(ctx, bk, progSock)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container: %w", err)
	}

	resource, _, _, err := env.execEnvironment(ctx, bk, progSock, envCache, installDeps, "", nil, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to exec environment: %w", err)
	}

	var ok bool
	env, ok = resource.(*Environment)
	if !ok {
		return nil, fmt.Errorf("environment initialization returned non-environment result")
	}

	return env, env.updateEnv()
}

func (env *Environment) FromConfig(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	envCache *EnvironmentCache,
	installDeps InstallDepsCallback,
	sourceDir *Directory,
	configPath string,
) (*Environment, error) {
	configPath = envconfig.NormalizeConfigPath(configPath)

	configFile, err := sourceDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}
	configBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg envconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	var eg errgroup.Group
	deps := make([]*Environment, len(cfg.Dependencies))
	for i, depURL := range cfg.Dependencies {
		i, depURL := i, depURL
		eg.Go(func() error {
			parsedURL, err := envconfig.ParseEnvURL(depURL)
			if err != nil {
				return fmt.Errorf("failed to parse dependency url %q: %w", depURL, err)
			}

			// TODO: In theory should first load *just* the config file, figure out the include/exclude, and then load everything else
			// based on that. That's not straightforward because we can't get the config file until we've loaded the dep...
			// May need to have `dagger env extend` and `dagger env sync` automatically include dependency include/exclude filters in
			// dagger.json.
			var depSourceDir *Directory
			var depConfigPath string
			switch {
			case parsedURL.Local != nil:
				depSourceDir = sourceDir
				depConfigPath = filepath.Join("/", filepath.Dir(configPath), parsedURL.Local.ConfigPath)
			case parsedURL.Git != nil:
				var err error
				depSourceDir, err = NewDirectorySt(ctx, llb.Git(parsedURL.Git.Repo, parsedURL.Git.Ref), "", env.Pipeline, env.Platform, nil)
				if err != nil {
					return fmt.Errorf("failed to create git directory: %w", err)
				}
				depConfigPath = parsedURL.Git.ConfigPath
			default:
				return fmt.Errorf("invalid dependency url from %q", depURL)
			}

			depEnv, err := env.FromConfig(ctx, bk, progSock, envCache, installDeps, depSourceDir, depConfigPath)
			if err != nil {
				return fmt.Errorf("failed to get dependency env from config %q: %w", depURL, err)
			}
			deps[i] = depEnv
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

	return env.From(ctx, bk, progSock, envCache, installDeps, cfg.Name, sourceDir, filepath.Dir(configPath), cfg.SDK, deps)
}

func (env *Environment) WithWorkdir(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	workdir *Directory,
) (*Environment, error) {
	env = env.Clone()
	env.Workdir = workdir
	return env, env.recalcRuntime(ctx, bk, progSock)
}

// recalculate the definition of the runtime based on the current state of the environment
func (env *Environment) recalcRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
) error {
	var runtime *Container
	var err error
	switch env.SDK {
	case envconfig.SDKGo:
		runtime, err = env.goRuntime(
			ctx,
			bk,
			progSock,
			env.SourceDirectory,
			env.SourceDirectorySubpath,
			env.Workdir,
		)
	case envconfig.SDKPython:
		runtime, err = env.pythonRuntime(
			ctx,
			bk,
			progSock,
			env.SourceDirectory,
			env.SourceDirectorySubpath,
			env.Workdir,
		)
	default:
		return fmt.Errorf("unknown sdk %q", env.SDK)
	}
	if err != nil {
		return fmt.Errorf("failed to get base runtime for sdk %s: %w", env.SDK, err)
	}

	env.Runtime = runtime
	return env.updateEnv()
}

type EntrypointInput struct {
	// The name of the entrypoint to invoke. If unset, then the environment
	// definition should be returned.
	Name string `json:"name"`

	// The arguments to pass to the entrypoint, serialized as json. The json
	// object maps argument names to argument values.
	Args string `json:"args"`
}

func (env *Environment) EntrypointInput(ctx context.Context, bk *buildkit.Client) (*EntrypointInput, error) {
	// TODO: error out if not coming from an env

	// TODO: doc, a bit silly looking but actually works out nicely
	inputBytes, err := bk.ReadCallerHostFile(ctx, filepath.Join(envMetaDirPath, envMetaInputPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read entrypoint input file: %w", err)
	}
	var input EntrypointInput
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entrypoint input: %w", err)
	}
	return &input, nil
}

func (env *Environment) ReturnEntrypointValue(ctx context.Context, valStr string, bk *buildkit.Client) error {
	// TODO: error out if not coming from an env

	// TODO: doc, a bit silly looking but actually works out nicely
	return bk.IOReaderExport(ctx, bytes.NewReader([]byte(valStr)), filepath.Join(envMetaDirPath, envMetaOutputPath), 0600)
}

type InstallDepsCallback func(context.Context, *Environment) error

func (env *Environment) execEnvironment(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	envCache *EnvironmentCache,
	installDeps InstallDepsCallback,
	entrypointName string,
	args any,
	cacheExitCode uint32,
) (any, []byte, uint32, error) {
	if err := installDeps(ctx, env); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to install deps: %w", err)
	}

	ctx, err := envCache.ContextWithCachedEnv(ctx, env)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to set environment in context: %w", err)
	}

	argsBytes, err := json.Marshal(args)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal args: %w", err)
	}

	ctr := env.Runtime

	metaDir := NewScratchDirectory(env.Pipeline, env.Platform)
	ctr, err = ctr.WithMountedDirectory(ctx, bk, envMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to mount env metadata directory: %w", err)
	}

	input := EntrypointInput{
		Name: entrypointName,
		Args: string(argsBytes),
	}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal input: %w", err)
	}
	inputFileDir, err := NewScratchDirectory(env.Pipeline, env.Platform).WithNewFile(ctx, envMetaInputPath, inputBytes, 0600, nil)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create input file: %w", err)
	}
	inputFile, err := inputFileDir.File(ctx, bk, envMetaInputPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to get input file: %w", err)
	}
	ctr, err = ctr.WithMountedFile(ctx, bk, filepath.Join(envMetaDirPath, envMetaInputPath), inputFile, "", true)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to mount input file: %w", err)
	}

	// Mount in read-only dep env filesystems to ensure that if they change, this env's cache is
	// also invalidated. Read-only forces buildkit to always use content-based cache keys.
	for _, dep := range env.Dependencies {
		dirMntPath := filepath.Join(envMetaDirPath, envMetaDepsDirPath, dep.Name, "dir")
		ctr, err = ctr.WithMountedDirectory(ctx, bk, dirMntPath, dep.SourceDirectory, "", true)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to mount dep directory: %w", err)
		}
		workdirMntPath := filepath.Join(envMetaDirPath, envMetaDepsDirPath, dep.Name, "workdir")
		ctr, err = ctr.WithMountedDirectory(ctx, bk, workdirMntPath, dep.Workdir, "", true)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to mount dep workdir: %w", err)
		}
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, env.Platform, ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
		CacheExitCode:                 cacheExitCode,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to exec entrypoint: %w", err)
	}
	ctrOutputDir, err := ctr.Directory(ctx, bk, envMetaDirPath)
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
		Filename: envMetaOutputPath,
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

// update existing entrypoints with the current state of their environment
func (env *Environment) updateEnv() error {
	envID, err := env.ID()
	if err != nil {
		return fmt.Errorf("failed to get environment ID: %w", err)
	}
	if err := env.setEntrypointEnvs(envID); err != nil {
		return fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return nil
}

func (id EnvironmentID) Decode() (*Environment, error) {
	env, err := resourceid.ID[Environment](id).Decode()
	if err != nil {
		return nil, err
	}
	if err := env.setEntrypointEnvs(id); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return env, nil
}

func (env *Environment) ID() (EnvironmentID, error) {
	env = env.Clone()
	if err := env.setEntrypointEnvs(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	id, err := resourceid.Encode(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode environment to id: %w", err)
	}
	return EnvironmentID(id), nil
}

func (env *Environment) Digest() (digest.Digest, error) {
	env = env.Clone()
	if err := env.setEntrypointEnvs(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return stableDigest(env)
}

func (env *Environment) setEntrypointEnvs(id EnvironmentID) error {
	// TODO: guard against infinite recursion
	curChecks := env.Checks
	for len(curChecks) > 0 {
		var nextChecks []*Check
		for _, check := range curChecks {
			nextChecks = append(nextChecks, check.Subchecks...)
			if check.EnvironmentID == "" {
				check.EnvironmentID = id
				continue
			}
			checkEnv, err := check.EnvironmentID.Decode()
			if err != nil {
				return fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
			}
			if checkEnv.Name == env.Name {
				check.EnvironmentID = id
			}
		}
		curChecks = nextChecks
	}
	return nil
}

type EnvironmentCache CacheMap[digest.Digest, *Environment]

func NewEnvironmentCache() *EnvironmentCache {
	return (*EnvironmentCache)(NewCacheMap[digest.Digest, *Environment]())
}

func (cache *EnvironmentCache) cacheMap() *CacheMap[digest.Digest, *Environment] {
	return (*CacheMap[digest.Digest, *Environment])(cache)
}

func (cache *EnvironmentCache) ContextWithCachedEnv(ctx context.Context, env *Environment) (context.Context, error) {
	envDigest, err := env.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment digest: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	clientMetadata.EnvironmentDigest = envDigest

	_, err = cache.cacheMap().GetOrInitialize(envDigest, func() (*Environment, error) {
		return env, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to cache environment: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}

func (cache *EnvironmentCache) CachedEnvFromContext(ctx context.Context) (*Environment, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	return cache.cacheMap().GetOrInitialize(clientMetadata.EnvironmentDigest, func() (*Environment, error) {
		return nil, fmt.Errorf("environment %s not found in cache", clientMetadata.EnvironmentDigest)
	})
}
