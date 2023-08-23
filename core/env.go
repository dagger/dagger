package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/envconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

const (
	inputMountPath = "/inputs"
	inputFile      = "/dagger.json"

	outputMountPath = "/outputs"
	outputFile      = "/dagger.json"

	envMetaDirPath = "/env"
	envIDFileName  = "id"
	EnvIDFile      = envMetaDirPath + "/" + envIDFileName

	envDepsPath = "/.deps"
)

type EnvironmentID string

func (id EnvironmentID) String() string {
	return string(id)
}

func (id EnvironmentID) ToEnvironment() (*Environment, error) {
	env := new(Environment)
	if id == "" {
		return env, nil
	}
	if err := resourceid.Decode(env, id); err != nil {
		return nil, err
	}
	if err := env.setEntrypointEnvs(id); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return env, nil
}

type Environment struct {
	// The environment's source code root directory
	Directory *Directory `json:"directory"`
	// Path to the environment's config file relative to the source root directory
	ConfigPath string `json:"configPath"`
	// The directory in which environment code executes as its current working directory
	Workdir *Directory `json:"workdir"`

	// The parsed environment config
	Config *envconfig.Config `json:"config"`

	// The environment's platform
	Platform specs.Platform `json:"platform,omitempty"`

	// TODO: doc, not in public api
	Runtime *Container `json:"runtime,omitempty"`

	// The environment's checks
	Checks []*Check `json:"checks,omitempty"`
}

func (env *Environment) Clone() *Environment {
	cp := *env
	if env.Directory != nil {
		cp.Directory = env.Directory.Clone()
	}
	if env.Workdir != nil {
		cp.Workdir = env.Workdir.Clone()
	}
	if env.Runtime != nil {
		cp.Runtime = env.Runtime.Clone()
	}
	if env.Config != nil {
		cp.Config = &envconfig.Config{
			Root:         env.Config.Root,
			Name:         env.Config.Name,
			SDK:          env.Config.SDK,
			Include:      cloneSlice(env.Config.Include),
			Exclude:      cloneSlice(env.Config.Exclude),
			Dependencies: cloneSlice(env.Config.Dependencies),
		}
	}
	cp.Checks = make([]*Check, len(env.Checks))
	for i, check := range env.Checks {
		cp.Checks[i] = check.Clone()
	}
	return &cp
}

func (env *Environment) ID() (EnvironmentID, error) {
	env = env.Clone()
	if err := env.setEntrypointEnvs(""); err != nil {
		return "", fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return resourceid.Encode[EnvironmentID](env)
}

// TODO: doc subtleties
func (env *Environment) Digest() (uint64, error) {
	env = env.Clone()
	if err := env.setEntrypointEnvs(""); err != nil {
		return 0, fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return cacheKey(env), nil
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
			checkEnv, err := check.EnvironmentID.ToEnvironment()
			if err != nil {
				return fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
			}
			if checkEnv.Config.Name == env.Config.Name {
				check.EnvironmentID = id
			}
		}
		curChecks = nextChecks
	}
	return nil
}

type EnvironmentCache CacheMap[uint64, *Environment]

func NewEnvironmentCache() *EnvironmentCache {
	return (*EnvironmentCache)(NewCacheMap[uint64, *Environment]())
}

func (cache *EnvironmentCache) cacheMap() *CacheMap[uint64, *Environment] {
	return (*CacheMap[uint64, *Environment])(cache)
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
		return nil, fmt.Errorf("environment %d not found in cache", clientMetadata.EnvironmentDigest)
	})
}

// Just load the config without actually getting the schema, useful for checking env metadata
// in an inexpensive way.
func LoadEnvironmentConfig(
	ctx context.Context,
	bk *buildkit.Client,
	rootDir *Directory,
	configPath string,
) (*envconfig.Config, error) {
	configPath = normalizeConfigPath(configPath)

	configFile, err := rootDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config at path %q: %w", configPath, err)
	}
	cfgBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment config at path %q: %w", configPath, err)
	}
	var cfg envconfig.Config
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal environment config: %w", err)
	}
	return &cfg, nil
}

func LoadEnvironment(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	envCache *EnvironmentCache,
	pipeline pipeline.Path,
	platform specs.Platform,
	deps []*Environment,
	rootDir *Directory,
	configPath string,
) (*Environment, error) {
	configPath = normalizeConfigPath(configPath)
	cfg, err := LoadEnvironmentConfig(ctx, bk, rootDir, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	env := &Environment{
		Directory:  rootDir,
		ConfigPath: configPath,
		Workdir:    rootDir, // TODO: make this actually configurable + enforced + better default
		Config:     cfg,
		Platform:   platform,
	}

	// add the base env to the context so CurrentEnvironment works in the exec below
	ctx, err = envCache.ContextWithCachedEnv(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to set environment in context: %w", err)
	}

	ctr, err := env.runtime(ctx, bk, progSock, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container: %w", err)
	}

	// Mount in read-only dep env filesystems to ensure that if they change, this env's cache is
	// also invalidated. Read-only forces buildkit to always use content-based cache keys.
	for _, dep := range deps {
		dirMntPath := filepath.Join(envDepsPath, dep.Config.Name, "dir")
		ctr, err = ctr.WithMountedDirectory(ctx, bk, dirMntPath, dep.Directory, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to mount dep directory: %w", err)
		}
		workdirMntPath := filepath.Join(envDepsPath, dep.Config.Name, "workdir")
		ctr, err = ctr.WithMountedDirectory(ctx, bk, workdirMntPath, dep.Workdir, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to mount dep workdir: %w", err)
		}
	}

	envMetaDir := NewScratchDirectory(pipeline, platform)
	ctr, err = ctr.WithMountedDirectory(ctx, bk, envMetaDirPath, envMetaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount env metadata directory: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args:                          []string{"-env"},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec env command: %w", err)
	}
	f, err := ctr.File(ctx, bk, EnvIDFile)
	if err != nil {
		return nil, fmt.Errorf("failed to get envid file: %w", err)
	}
	envID, err := f.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read envid file: %w", err)
	}
	env, err = EnvironmentID(envID).ToEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to decode envid: %w", err)
	}

	// finalize the environment's container where entrypoint code executes
	ctr, err = ctr.WithoutMount(ctx, envMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to unmount env metadata directory: %w", err)
	}
	env.Runtime = ctr

	finalEnvID, err := env.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment ID: %w", err)
	}
	if err := env.setEntrypointEnvs(finalEnvID); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint envs: %w", err)
	}

	return env, nil
}

// figure out if we were passed a path to a dagger.json file or a parent dir that may contain such a file
func normalizeConfigPath(configPath string) string {
	baseName := path.Base(configPath)
	if baseName == "dagger.json" {
		return configPath
	}
	return path.Join(configPath, "dagger.json")
}

func (env *Environment) runtime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
) (*Container, error) {
	switch env.Config.SDK {
	case envconfig.SDKGo:
		return goRuntime(ctx, bk, progSock, pipeline, env.Platform, env.Directory, env.ConfigPath)
	case envconfig.SDKPython:
		return pythonRuntime(ctx, bk, progSock, pipeline, env.Platform, env.Directory, env.ConfigPath)
	default:
		return nil, fmt.Errorf("unknown sdk %q", env.Config.SDK)
	}
}

func execEntrypoint(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	envCache *EnvironmentCache,
	env *Environment,
	entrypointName string,
	args any,
) ([]byte, error) {
	ctx, err := envCache.ContextWithCachedEnv(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to set environment in context: %w", err)
	}

	inputMap := map[string]any{
		// TODO: remember to tell Helder that this is a small breaking change, need to tweak python sdk code.
		// "resolver" used to be in form <parent>.<field>, now its just the name of the entrypoint (i.e. check
		// name, artifact name, etc.)
		"resolver": entrypointName,
		"args":     args,
		"parent":   nil, // for now, could support parent data in future for user-defined chainable types
	}
	inputBytes, err := json.Marshal(inputMap)
	if err != nil {
		return nil, err
	}
	ctr, err := env.Runtime.WithNewFile(ctx, bk, filepath.Join(inputMountPath, inputFile), inputBytes, 0644, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount entrypoint input file: %w", err)
	}

	ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(pipeline, ctr.Platform), "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount entrypoint output directory: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, ctr.Platform, ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec entrypoint: %w", err)
	}
	err = ctr.Evaluate(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate entrypoint: %w", err)
	}

	outputFile, err := ctr.File(ctx, bk, filepath.Join(outputMountPath, outputFile))
	if err != nil {
		return nil, fmt.Errorf("failed to get entrypoint output file: %w", err)
	}

	entrypointOutput, err := outputFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read entrypoint output file: %w", err)
	}

	return entrypointOutput, nil
}

func (env *Environment) WithCheck(check *Check, envCache *EnvironmentCache) (*Environment, error) {
	env = env.Clone()
	env.Checks = append(env.Checks, check)
	envID, err := env.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment ID: %w", err)
	}
	if err := env.setEntrypointEnvs(envID); err != nil {
		return nil, fmt.Errorf("failed to set entrypoint envs: %w", err)
	}
	return env, nil
}

type CheckID string

func (id CheckID) String() string {
	return string(id)
}

func (id CheckID) ToCheck() (*Check, error) {
	var check Check
	if id == "" {
		return &check, nil
	}
	if err := resourceid.Decode(&check, id); err != nil {
		return nil, err
	}
	if !check.IsCheck {
		return nil, fmt.Errorf("resource %q is not a check", id)
	}
	return &check, nil
}

type Check struct {
	EnvironmentID EnvironmentID `json:"environmentId,omitempty"`

	Name        string   `json:"name"`
	Description string   `json:"description"`
	Subchecks   []*Check `json:"subchecks"`

	// The container to exec if the check's success+output is being resolved
	// from a user-defined container via the Check.withContainer API
	UserContainer *Container `json:"user_container"`

	// TODO: backport generalize polymorphic safety checks
	IsCheck bool `json:"is_check"`
}

func (check *Check) ID() (CheckID, error) {
	check.IsCheck = true
	return resourceid.Encode[CheckID](check)
}

func (check *Check) Digest() (digest.Digest, error) {
	return stableDigest(check)
}

func (check Check) Clone() *Check {
	cp := Check{
		Name:          check.Name,
		Description:   check.Description,
		Subchecks:     make([]*Check, len(check.Subchecks)),
		EnvironmentID: check.EnvironmentID,
		IsCheck:       check.IsCheck,
	}
	for i, subcheck := range check.Subchecks {
		cp.Subchecks[i] = subcheck.Clone()
	}
	if check.UserContainer != nil {
		cp.UserContainer = check.UserContainer.Clone()
	}
	return &cp
}

func (check *Check) WithName(name string) *Check {
	check = check.Clone()
	check.Name = name
	return check
}

func (check *Check) WithDescription(description string) *Check {
	check = check.Clone()
	check.Description = description
	return check
}

func (check *Check) WithSubcheck(subcheck *Check) *Check {
	check = check.Clone()
	check.Subchecks = append(check.Subchecks, subcheck)
	return check
}

func (check *Check) WithUserContainer(ctr *Container) *Check {
	check = check.Clone()
	check.UserContainer = ctr
	return check
}

func (check *Check) GetSubchecks(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	envCache *EnvironmentCache,
) ([]*Check, error) {
	if len(check.Subchecks) > 0 {
		return check.Subchecks, nil
	}

	if check.EnvironmentID != "" {
		env, err := check.EnvironmentID.ToEnvironment()
		if err != nil {
			return nil, fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
		}
		entrypointOutput, err := execEntrypoint(ctx, bk, progSock, pipeline, envCache, env, check.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to exec check environment container: %w", err)
		}

		// TODO: comments once finalized, also backport polymorphic safety checks
		var id string
		if err := json.Unmarshal(entrypointOutput, &id); err != nil {
			return nil, fmt.Errorf("failed to decode check result from environment container: %w", err)
		}

		recursiveCheck, err := CheckID(id).ToCheck()
		if err != nil {
			// not a recursive check, there are no subchecks
			return nil, nil
		}
		return recursiveCheck.GetSubchecks(ctx, bk, progSock, pipeline, envCache)
	}

	return nil, nil
}

func (check *Check) Result(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	envCache *EnvironmentCache,
) (*CheckResult, error) {
	if len(check.Subchecks) > 0 {
		// This is a composite check, evaluate it by evaluating each subcheck
		var eg errgroup.Group
		success := true
		var output string
		for _, subcheck := range check.Subchecks {
			subcheck := subcheck
			eg.Go(func() error {
				subresult, err := subcheck.Result(ctx, bk, progSock, pipeline, envCache)
				if err != nil {
					return fmt.Errorf("failed to get subcheck result for %q: %w", subcheck.Name, err)
				}
				if !subresult.Success {
					success = false
					output += fmt.Sprintf("Subcheck %q failed:\n%s\n", subcheck.Name, subresult.Output)
				} else {
					output += fmt.Sprintf("Subcheck %q succeeded:\n%s\n", subcheck.Name, subresult.Output)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		return &CheckResult{
			Success: success,
			Output:  "",
		}, nil
	}

	if check.UserContainer != nil {
		// check will be evaluated by exec'ing this container, with success based on exit code
		ctr := check.UserContainer
		success := true
		var output string
		evalErr := ctr.Evaluate(ctx, bk)
		var execErr *buildkit.ExecError
		switch {
		case errors.As(evalErr, &execErr):
			success = false
			// TODO: really need combined stdout/stderr now
			output = strings.Join([]string{evalErr.Error(), execErr.Stdout, execErr.Stderr}, "\n\n")
		case evalErr != nil:
			return nil, fmt.Errorf("failed to exec check user container: %w", evalErr)
		default:
			stdout, err := ctr.MetaFileContents(ctx, bk, progSock, "stdout")
			if err != nil {
				return nil, fmt.Errorf("failed to get stdout from check user container: %w", err)
			}
			stderr, err := ctr.MetaFileContents(ctx, bk, progSock, "stderr")
			if err != nil {
				return nil, fmt.Errorf("failed to get stderr from check user container: %w", err)
			}
			// TODO: really need combined stdout/stderr now
			output = strings.Join([]string{stdout, stderr}, "\n\n")
		}
		return &CheckResult{
			Success: success,
			Output:  output,
		}, nil
	}

	if check.EnvironmentID != "" {
		// check will be evaluated by exec'ing the environment's resolver
		env, err := check.EnvironmentID.ToEnvironment()
		if err != nil {
			return nil, fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
		}
		entrypointOutput, err := execEntrypoint(ctx, bk, progSock, pipeline, envCache, env, check.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to exec check environment container: %w", err)
		}

		// TODO: comments once finalized, also backport polymorphic safety checks
		var id string
		if err := json.Unmarshal(entrypointOutput, &id); err != nil {
			return nil, fmt.Errorf("failed to decode check result from environment container: %w", err)
		}

		checkResult, err := CheckResultID(id).ToCheckResult()
		if err == nil {
			return checkResult, nil
		}

		recursiveCheck, err := CheckID(id).ToCheck()
		if err != nil {
			return nil, fmt.Errorf("failed to decode check from environment container: %w", err)
		}
		res, err := recursiveCheck.Result(ctx, bk, progSock, pipeline, envCache)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate recursive check: %w", err)
		}
		return res, nil
	}

	return nil, fmt.Errorf("invalid empty check %q", check.Name)
}

type CheckResultID string

func (id CheckResultID) String() string {
	return string(id)
}

func (id CheckResultID) ToCheckResult() (*CheckResult, error) {
	var checkResult CheckResult
	if id == "" {
		return &checkResult, nil
	}
	if err := resourceid.Decode(&checkResult, id); err != nil {
		return nil, err
	}
	if !checkResult.IsCheckResult {
		return nil, fmt.Errorf("invalid check result ID %q", id)
	}
	return &checkResult, nil
}

type CheckResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	// TODO: backport generalize polymorphic safety checks
	IsCheckResult bool `json:"is_check_result"`
}

func (checkResult *CheckResult) ID() (CheckResultID, error) {
	checkResult.IsCheckResult = true
	return resourceid.Encode[CheckResultID](checkResult)
}

func (checkResult *CheckResult) Digest() (digest.Digest, error) {
	return stableDigest(checkResult)
}

func (checkResult CheckResult) Clone() *CheckResult {
	cp := checkResult
	return &cp
}
