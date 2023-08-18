package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"

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
)

type EnvironmentID string

func (id EnvironmentID) String() string {
	return string(id)
}

func (id EnvironmentID) ToEnvironment() (*Environment, error) {
	var environment Environment
	if id == "" {
		return &environment, nil
	}
	if err := resourceid.Decode(&environment, id); err != nil {
		return nil, err
	}
	return &environment, nil
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

	// The environment's checks
	Checks []*Check `json:"checks,omitempty"`
}

func NewEnvironment(id EnvironmentID) (*Environment, error) {
	environment, err := id.ToEnvironment()
	if err != nil {
		return nil, err
	}
	return environment, nil
}

func (env *Environment) ID() (EnvironmentID, error) {
	return resourceid.Encode[EnvironmentID](env)
}

func (env *Environment) Clone() *Environment {
	cp := *env
	if env.Directory != nil {
		cp.Directory = env.Directory.Clone()
	}
	if env.Config != nil {
		env.Config = &envconfig.Config{
			Root:         env.Config.Root,
			Name:         env.Config.Name,
			SDK:          env.Config.SDK,
			Include:      cloneSlice(env.Config.Include),
			Exclude:      cloneSlice(env.Config.Exclude),
			Dependencies: cloneSlice(env.Config.Dependencies),
		}
	}
	for i, check := range env.Checks {
		cp.Checks[i] = check.Clone()
	}
	return &cp
}

// Just load the config without actually getting the schema, useful for checking env metadata
// in an inexpensive way
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
	pipeline pipeline.Path,
	platform specs.Platform,
	rootDir *Directory,
	configPath string,
) (*Environment, error) {
	configPath = normalizeConfigPath(configPath)
	cfg, err := LoadEnvironmentConfig(ctx, bk, rootDir, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	clientMetadata.EnvironmentName = cfg.Name
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	envRuntime, err := runtime(ctx, bk, progSock, pipeline, platform, cfg.Name, cfg.SDK, rootDir, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container: %w", err)
	}
	ctr, err := envRuntime.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(pipeline, platform), "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount output directory: %w", err)
	}

	// ask the environment for its base config (commands, etc.)
	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args:                          []string{"-env"},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec schema command: %w", err)
	}
	f, err := ctr.File(ctx, bk, "/outputs/envid")
	if err != nil {
		return nil, fmt.Errorf("failed to get envid file: %w", err)
	}
	newEnvID, err := f.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read envid file: %w", err)
	}
	env, err := EnvironmentID(newEnvID).ToEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to decode envid: %w", err)
	}

	// fill in the other stuff we know about the environment
	env.Directory = rootDir
	env.ConfigPath = configPath
	env.Workdir = rootDir // TODO: make this actually configurable + enforced + better default
	env.Config = cfg
	env.Platform = platform
	if err != nil {
		return nil, fmt.Errorf("failed to build schema: %w", err)
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

func runtime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	platform specs.Platform,
	envName string,
	sdk envconfig.SDK,
	rootDir *Directory,
	configPath string,
) (*Container, error) {
	var ctr *Container
	var err error
	switch envconfig.SDK(sdk) {
	case envconfig.SDKGo:
		ctr, err = goRuntime(ctx, bk, progSock, pipeline, platform, rootDir, configPath)
	case envconfig.SDKPython:
		ctr, err = pythonRuntime(ctx, bk, progSock, pipeline, platform, rootDir, configPath)
	default:
		return nil, fmt.Errorf("unknown sdk %q", sdk)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container: %w", err)
	}
	ctr.EnvironmentName = envName
	return ctr, nil
}

func execResolverContainer(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	ctr *Container,
	entrypointName string,
	args any,
) (resolverOutput []byte, stdout, stderr string, rerr error) {
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
		return nil, "", "", err
	}
	ctr, err = ctr.WithNewFile(ctx, bk, filepath.Join(inputMountPath, inputFile), inputBytes, 0644, "")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to mount resolver input file: %w", err)
	}

	ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(nil, ctr.Platform), "")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to mount resolver output directory: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, ctr.Platform, ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to exec resolver: %w", err)
	}
	err = ctr.Evaluate(ctx, bk)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to exec resolver: %w", err)
	}

	outputFile, err := ctr.File(ctx, bk, filepath.Join(outputMountPath, outputFile))
	if err == nil {
		// TODO: would be better to check "file not found" error specifically
		resolverOutput, err = outputFile.Contents(ctx, bk)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to read resolver output file: %w", err)
		}
	}

	stdout, err = ctr.MetaFileContents(ctx, bk, progSock, "stdout")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to read resolver stdout: %w", err)
	}
	stderr, err = ctr.MetaFileContents(ctx, bk, progSock, "stderr")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to read resolver stderr: %w", err)
	}
	return resolverOutput, stdout, stderr, nil
}

func (env *Environment) WithCheck(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	check *Check,
) (*Environment, error) {
	env = env.Clone()
	if check.Result == "" {
		// by default, determine the check result by executing the environment resolver
		ctr, err := runtime(
			ctx,
			bk,
			progSock,
			pipeline,
			env.Platform,
			env.Config.Name,
			env.Config.SDK,
			env.Directory,
			env.ConfigPath,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build runtime: %w", err)
		}
		ctrID, err := ctr.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get runtime container ID: %w", err)
		}
		check, err = check.WithContainer(ctrID)
		if err != nil {
			return nil, fmt.Errorf("failed to set check container ID: %w", err)
		}
	}
	env.Checks = append(env.Checks, check)
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
	return &check, nil
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
	return &checkResult, nil
}

type Check struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Subchecks   []CheckID `json:"subchecks"`

	// The result, lazily represented as an id
	Result CheckResultID `json:"result"`
}

func NewCheck(id CheckID) (*Check, error) {
	check, err := id.ToCheck()
	if err != nil {
		return nil, err
	}
	return check, nil
}

func (check *Check) ID() (CheckID, error) {
	return resourceid.Encode[CheckID](check)
}

func (check *Check) Digest() (digest.Digest, error) {
	return stableDigest(check)
}

func (check Check) Clone() *Check {
	cp := check
	cp.Subchecks = cloneSlice(check.Subchecks)
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

func (check *Check) WithSubcheck(subcheckID CheckID) (*Check, error) {
	check = check.Clone()
	check.Subchecks = append(check.Subchecks, subcheckID)

	// update the result to include the subcheck result so it knows to
	// include it in the final result when evaluated
	result, err := check.Result.ToCheckResult()
	if err != nil {
		return nil, fmt.Errorf("failed to decode check result in withContainer: %w", err)
	}
	subcheck, err := subcheckID.ToCheck()
	if err != nil {
		return nil, fmt.Errorf("failed to decode subcheck in withContainer: %w", err)
	}
	result.Subresults = append(result.Subresults, subcheck.Result)
	resultID, err := result.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to encode check result in withContainer: %w", err)
	}
	check.Result = resultID

	return check, nil
}

func (check *Check) WithContainer(containerID ContainerID) (*Check, error) {
	check = check.Clone()
	result, err := check.Result.ToCheckResult()
	if err != nil {
		return nil, fmt.Errorf("failed to decode check result in withContainer: %w", err)
	}
	result.Container = containerID
	resultID, err := result.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to encode check result in withContainer: %w", err)
	}
	check.Result = resultID
	return check, nil
}

type CheckResult struct {
	// The name of the check this result is for
	// TODO: a content addressed ID would be better, but need to update SDKs to handle that
	Name string `json:"name"`

	// If this result is statically defined (i.e. not evaluated in a container)
	// StaticSuccess is the success value of the result
	StaticSuccess bool `json:"success"`

	// If this result is statically defined (i.e. not evaluated in a container)
	// StaticOutput is the output value of the result
	StaticOutput string `json:"output"`

	// If set, the container that will be exec'd to evaluate the result
	Container ContainerID `json:"container"`

	// If set, the result will be evaluated by evaluating each of the subresults
	// NOTE: this is only used internally, not part of public API
	Subresults []CheckResultID `json:"subresults"`
}

func NewCheckResult(id CheckResultID) (*CheckResult, error) {
	checkRes, err := id.ToCheckResult()
	if err != nil {
		return nil, err
	}
	return checkRes, nil
}

func NewStaticCheckResult(name string, success bool, output string) *CheckResult {
	return &CheckResult{
		Name:          name,
		StaticSuccess: success,
		StaticOutput:  output,
	}
}

func (result *CheckResult) ID() (CheckResultID, error) {
	return resourceid.Encode[CheckResultID](result)
}

func (result *CheckResult) Clone() *CheckResult {
	cp := *result
	return &cp
}

func (result *CheckResult) Success(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
) (bool, error) {
	success, _, err := result.evaluate(ctx, bk, progSock, pipeline)
	return success, err
}

func (result *CheckResult) Output(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
) (string, error) {
	_, output, err := result.evaluate(ctx, bk, progSock, pipeline)
	return output, err
}

func (result *CheckResult) evaluate(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
) (bool, string, error) {
	if len(result.Subresults) > 0 {
		// this is a composite result, evaluate it by evaluating each subresult
		var eg errgroup.Group
		// TODO: trying to combine output is gonna be a mess, it's currently up to the
		// clients (i.e. cli, webui) to instead get subchecks and display them nicely
		success := true
		for _, subresultID := range result.Subresults {
			subresultID := subresultID
			eg.Go(func() error {
				subresult, err := subresultID.ToCheckResult()
				if err != nil {
					return fmt.Errorf("failed to decode subresult in evaluate: %w", err)
				}
				subsuccess, _, err := subresult.evaluate(ctx, bk, progSock, pipeline)
				if err != nil {
					return err
				}
				if !subsuccess {
					success = false
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return false, "", err
		}
		return success, "", nil
	}

	if result.Container == "" {
		// result is statically defined
		return result.StaticSuccess, result.StaticOutput, nil
	}

	// result will come from exec'ing a container
	ctr, err := result.Container.ToContainer()
	if err != nil {
		return false, "", fmt.Errorf("failed to decode container in check result evaluate: %w", err)
	}
	resolverOutput, stdout, stderr, err := execResolverContainer(ctx, bk, progSock, pipeline, ctr, result.Name, nil)
	var execErr *buildkit.ExecError
	switch {
	case errors.As(err, &execErr):
		stdout = execErr.Stdout
		stderr = execErr.Stderr
	case err != nil:
		return false, "", fmt.Errorf("failed to exec resolver container: %w", err)
	}

	if resolverOutput != nil {
		// The result is being returned from an env resolver as a serialized CheckResult.
		// Deserialize and get its result.
		res, err := CheckResultID(resolverOutput).ToCheckResult()
		if err != nil {
			return false, "", fmt.Errorf("failed to decode check result resolver output: %w", err)
		}
		return res.evaluate(ctx, bk, progSock, pipeline)
	}

	// The container didn't give us a result, so we'll just use the exit code and stdout/stderr
	if execErr == nil {
		return true, stdout, nil // TODO: should we combine stdout/stderr somehow?
	}
	return false, stderr, nil // TODO: should we combine stdout/stderr somehow?
}
