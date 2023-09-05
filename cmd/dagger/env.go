package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

var (
	environmentURL   string
	environmentFlags = pflag.NewFlagSet("environment", pflag.ContinueOnError)

	sdk             string
	environmentName string
	environmentRoot string
)

const (
	environmentURLDefault = "."
)

func init() {
	environmentFlags.StringVarP(&environmentURL, "env", "e", environmentURLDefault, "Path to dagger.json config file for the environment or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger?ref=branch?subpath=path/to/some/dir\").")
	environmentFlags.BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	environmentCmd.PersistentFlags().AddFlagSet(environmentFlags)
	checkCmd.PersistentFlags().AddFlagSet(environmentFlags)
	listenCmd.PersistentFlags().AddFlagSet(environmentFlags)
	queryCmd.PersistentFlags().AddFlagSet(environmentFlags)

	environmentInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK to use for the environment")
	environmentInitCmd.MarkFlagRequired("sdk")
	environmentInitCmd.PersistentFlags().StringVar(&environmentName, "name", "", "Name of the new environment")
	environmentInitCmd.MarkFlagRequired("name")
	environmentInitCmd.PersistentFlags().StringVarP(&environmentRoot, "root", "", "", "Root directory that should be loaded for the full environment context. Defaults to the parent directory containing dagger.json.")
	// also include codegen flags since codegen will run on environment init

	environmentCmd.AddCommand(environmentInitCmd)
	environmentCmd.AddCommand(environmentExtendCmd)
	environmentCmd.AddCommand(environmentSyncCmd)
}

var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env"},
	Short:   "Manage dagger environments",
	Long:    "Manage dagger environments. By default, print the configuration of the specified environment in json format.",
	Hidden:  true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		env, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		var cfg *moduleconfig.Config
		switch {
		case env.local != nil:
			cfg, err = env.local.config()
			if err != nil {
				return fmt.Errorf("failed to get local environment config: %w", err)
			}
		case env.git != nil:
			// we need to read the git repo, which currently requires an engine+client
			err = withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
				rec := progrock.RecorderFromContext(ctx)
				vtx := rec.Vertex("get-env-config", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()
				readConfigTask := vtx.Task("reading git environment config")
				cfg, err = env.git.config(ctx, engineClient.Dagger())
				readConfigTask.Done(err)
				if err != nil {
					return fmt.Errorf("failed to get git environment config: %w", err)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal environment config: %w", err)
		}
		cmd.Println(string(cfgBytes))
		return nil
	},
}

var environmentInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger environment in a local directory.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		ctx := cmd.Context()

		env, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		if env != nil {
			if env.git != nil {
				return fmt.Errorf("environment init is not supported for git environments")
			}
			if _, err := os.Stat(env.local.path); err == nil {
				return fmt.Errorf("environment init config path already exists: %s", env.local.path)
			}
		}

		cfg := &moduleconfig.Config{
			Name: environmentName,
			SDK:  moduleconfig.SDK(sdk),
			Root: environmentRoot,
		}

		return updateEnvironmentConfig(ctx, env.local.path, cfg, cmd)
	},
}

var environmentExtendCmd = &cobra.Command{
	Use:    "extend",
	Short:  "Extend a dagger environment with access to the entrypoints of another environment",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		envFlagCfg, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		if envFlagCfg.git != nil {
			return fmt.Errorf("environment extend is not supported for git environments")
		}
		envCfg, err := envFlagCfg.config(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get environment config: %w", err)
		}

		depSet := make(map[string]struct{})
		for _, dep := range envCfg.Dependencies {
			depSet[dep] = struct{}{}
		}
		for _, newDep := range extraArgs {
			depEnvFlagCfg, err := getEnvironmentFlagConfigFromURL(newDep)
			if err != nil {
				return fmt.Errorf("failed to get environment: %w", err)
			}
			switch {
			case depEnvFlagCfg.local != nil:
				depPath, err := filepath.Rel(filepath.Dir(envFlagCfg.local.path), filepath.Dir(depEnvFlagCfg.local.path))
				if err != nil {
					return fmt.Errorf("failed to get relative path for dependency: %w", err)
				}
				depSet[depPath] = struct{}{}
			case depEnvFlagCfg.git != nil:
				gitURL, err := depEnvFlagCfg.git.urlString()
				if err != nil {
					return fmt.Errorf("failed to get git url for dependency: %w", err)
				}
				depSet[gitURL] = struct{}{}
			}
		}

		envCfg.Dependencies = nil
		for dep := range depSet {
			envCfg.Dependencies = append(envCfg.Dependencies, dep)
		}
		sort.Strings(envCfg.Dependencies)

		return updateEnvironmentConfig(ctx, envFlagCfg.local.path, envCfg, cmd)
	},
}

var environmentSyncCmd = &cobra.Command{
	Use:    "sync",
	Short:  "Synchronize a dagger environment with the latest version of its extensions",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		envFlagCfg, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		if envFlagCfg.git != nil {
			return fmt.Errorf("environment sync is not supported for git environments")
		}
		envCfg, err := envFlagCfg.config(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get environment config: %w", err)
		}
		return updateEnvironmentConfig(ctx, envFlagCfg.local.path, envCfg, cmd)
	},
}

func updateEnvironmentConfig(
	ctx context.Context,
	path string,
	newEnvCfg *moduleconfig.Config,
	cmd *cobra.Command,
) (rerr error) {
	runCodegenFunc := func() error {
		return nil
	}
	switch moduleconfig.SDK(newEnvCfg.SDK) {
	case moduleconfig.SDKGo:
		runCodegenFunc = func() error {
			return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
				rec := progrock.RecorderFromContext(ctx)
				vtx := rec.Vertex("env-update", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()

				loadDeps := vtx.Task("loading environment dependencies")
				envFlagCfg := &environmentFlagConfig{local: &localEnvironment{path: path}}
				deps, err := envFlagCfg.loadDeps(ctx, engineClient.Dagger())
				loadDeps.Done(err)
				if err != nil {
					return fmt.Errorf("failed to load dependencies: %w", err)
				}
				runCodegenTask := vtx.Task("generating environment code")
				err = RunCodegen(ctx, engineClient, nil, newEnvCfg, deps, cmd, nil)
				runCodegenTask.Done(err)
				return err
			})
		}
	case moduleconfig.SDKPython:
	default:
		return fmt.Errorf("unsupported environment SDK: %s", sdk)
	}

	cfgBytes, err := json.MarshalIndent(newEnvCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal environment config: %w", err)
	}
	parentDir := filepath.Dir(path)
	_, parentDirStatErr := os.Stat(parentDir)
	switch {
	case parentDirStatErr == nil:
		// already exists, nothing to do
	case os.IsNotExist(parentDirStatErr):
		// make the parent dir, but if something goes wrong, clean it up in the defer
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create environment config directory: %w", err)
		}
		defer func() {
			if rerr != nil {
				os.RemoveAll(parentDir)
			}
		}()
	default:
		return fmt.Errorf("failed to stat parent directory: %w", parentDirStatErr)
	}

	var cfgFileMode os.FileMode = 0644
	originalContents, configFileReadErr := os.ReadFile(path)
	switch {
	case configFileReadErr == nil:
		// attempt to restore the original file if it already existed and something goes wrong
		stat, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("failed to stat environment config: %w", err)
		}
		cfgFileMode = stat.Mode()
		defer func() {
			if rerr != nil {
				os.WriteFile(path, originalContents, cfgFileMode)
			}
		}()
	case os.IsNotExist(configFileReadErr):
		// remove it if it didn't exist already and something goes wrong
		defer func() {
			if rerr != nil {
				os.RemoveAll(path)
			}
		}()
	default:
		return fmt.Errorf("failed to read environment config: %w", configFileReadErr)
	}

	// nolint:gosec
	if err := os.WriteFile(path, append(cfgBytes, '\n'), cfgFileMode); err != nil {
		return fmt.Errorf("failed to write environment config: %w", err)
	}

	if err := runCodegenFunc(); err != nil {
		return fmt.Errorf("failed to run codegen: %w", err)
	}

	return nil
}

func getEnvironmentFlagConfig() (*environmentFlagConfig, error) {
	environmentURL := environmentURL
	if environmentURL == "" || environmentURL == environmentURLDefault {
		// it's unset or default value, use env if present
		if v, ok := os.LookupEnv("DAGGER_ENV"); ok {
			environmentURL = v
		}
	}
	return getEnvironmentFlagConfigFromURL(environmentURL)
}

func getEnvironmentFlagConfigFromURL(environmentURL string) (*environmentFlagConfig, error) {
	parsedURL, err := moduleconfig.ParseModuleURL(environmentURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse environment URL: %w", err)
	}
	switch {
	case parsedURL.Local != nil:
		localPath, err := filepath.Abs(parsedURL.Local.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get environment absolute path: %w", err)
		}
		return &environmentFlagConfig{local: &localEnvironment{
			path: localPath,
		}}, nil
	case parsedURL.Git != nil:
		return &environmentFlagConfig{git: &gitEnvironment{
			repo:    parsedURL.Git.Repo,
			ref:     parsedURL.Git.Ref,
			subpath: parsedURL.Git.ConfigPath,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported environment URL: %q", environmentURL)
	}
}

// environmentFlagConfig holds the environment settings provided by the user via flags (or defaults if not set)
type environmentFlagConfig struct {
	// only one of these will be set
	local *localEnvironment
	git   *gitEnvironment
}

func (p environmentFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Environment, error) {
	var env *dagger.Environment
	var err error
	switch {
	case p.local != nil:
		env, err = p.local.load(c)
	case p.git != nil:
		env, err = p.git.load(ctx, c)
	default:
		return nil, fmt.Errorf("invalid environment")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load environment: %w", err)
	}
	// install the env schema too
	// TODO: fix codegen, it's requiring EnvironmentID rather than *Environment for some reason
	envID, err := env.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment ID: %w", err)
	}
	if _, err := c.InstallEnvironment(ctx, envID); err != nil {
		return nil, fmt.Errorf("failed to install environment: %w", err)
	}
	return env, nil
}

func (p environmentFlagConfig) config(ctx context.Context, c *dagger.Client) (*moduleconfig.Config, error) {
	switch {
	case p.local != nil:
		return p.local.config()
	case p.git != nil:
		return p.git.config(ctx, c)
	default:
		panic("invalid environment")
	}
}

func (p environmentFlagConfig) loadDeps(ctx context.Context, c *dagger.Client) ([]*dagger.Environment, error) {
	if p.local == nil {
		return nil, fmt.Errorf("TODO: implement non-local environment dependency loading")
	}

	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment config: %w", err)
	}

	var depEnvs []*dagger.Environment
	for _, dep := range cfg.Dependencies {
		depPath := filepath.Join(filepath.Dir(p.local.path), dep)
		if filepath.Base(depPath) != "dagger.json" {
			depPath = filepath.Join(depPath, "dagger.json")
		}
		depEnv, err := localEnvironment{path: depPath}.load(c)
		if err != nil {
			return nil, fmt.Errorf("failed to load dependency environment: %w", err)
		}
		depEnvs = append(depEnvs, depEnv)
	}

	var eg errgroup.Group
	for _, depEnv := range depEnvs {
		depEnv := depEnv
		eg.Go(func() error {
			depEnvID, err := depEnv.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get dependency environment id %w", err)
			}
			_, err = c.InstallEnvironment(ctx, depEnvID)
			if err != nil {
				return fmt.Errorf("failed to install dependency environment %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return depEnvs, nil
}

func (p environmentFlagConfig) envExists(ctx context.Context, c *dagger.Client) (bool, error) {
	switch {
	case p.local != nil:
		return p.local.envExists()
	case p.git != nil:
		return p.git.envExists(ctx, c)
	default:
		return false, fmt.Errorf("invalid environment")
	}
}

type localEnvironment struct {
	path string
}

func (p localEnvironment) config() (*moduleconfig.Config, error) {
	configBytes, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read local config file: %w", err)
	}
	var cfg moduleconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse local config file: %w", err)
	}
	return &cfg, nil
}

func (p localEnvironment) load(c *dagger.Client) (*dagger.Environment, error) {
	rootDir, err := p.rootDir()
	if err != nil {
		return nil, err
	}
	subdirRelPath, err := filepath.Rel(rootDir, p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
	}
	if strings.HasPrefix(subdirRelPath, "..") {
		return nil, fmt.Errorf("environment config path %q is not under environment root %q", p.path, rootDir)
	}
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	hostDir := c.Host().Directory(rootDir, dagger.HostDirectoryOpts{
		Include: cfg.Include,
		Exclude: cfg.Exclude,
	})
	return c.Environment().FromConfig(hostDir, dagger.EnvironmentFromConfigOpts{
		ConfigPath: subdirRelPath,
	}), nil
}

func (p localEnvironment) rootDir() (string, error) {
	cfg, err := p.config()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(p.path), cfg.Root)), nil
}

func (p localEnvironment) envExists() (bool, error) {
	_, err := os.Stat(p.path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to stat environment config: %w", err)
}

type gitEnvironment struct {
	subpath string
	repo    string
	ref     string
}

func (p gitEnvironment) config(ctx context.Context, c *dagger.Client) (*moduleconfig.Config, error) {
	if c == nil {
		return nil, fmt.Errorf("cannot load git environment config with nil dagger client")
	}
	configStr, err := c.Git(p.repo).Branch(p.ref).Tree().File(p.subpath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read git config file: %w", err)
	}
	var cfg moduleconfig.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse git config file: %w", err)
	}
	return &cfg, nil
}

func (p gitEnvironment) load(ctx context.Context, c *dagger.Client) (*dagger.Environment, error) {
	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, err
	}
	rootPath := filepath.Clean(filepath.Join(filepath.Dir(p.subpath), cfg.Root))
	subdirRelPath, err := filepath.Rel(rootPath, p.subpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
	}
	if strings.HasPrefix(subdirRelPath, "..") {
		return nil, fmt.Errorf("environment config path %q is not under environment root %q", p.subpath, rootPath)
	}
	return c.Environment().FromConfig(
		c.Git(p.repo).Branch(p.ref).Tree().Directory(rootPath), dagger.EnvironmentFromConfigOpts{
			ConfigPath: subdirRelPath,
		}), nil
}

func (p gitEnvironment) envExists(ctx context.Context, c *dagger.Client) (bool, error) {
	_, err := c.Git(p.repo).Branch(p.ref).Tree().File(p.subpath).Sync(ctx)
	// TODO: this could technically fail for other reasons, but is okay enough for now, it will
	// still fail later if something else went wrong
	return err == nil, nil
}

// convert back to url string (with normalization after previous parsing)
func (p gitEnvironment) urlString() (string, error) {
	repoURL, err := url.Parse(p.repo)
	if err != nil {
		return "", fmt.Errorf("failed to parse repo url: %w", err)
	}

	gitURL := url.URL{
		Scheme: "git",
		Host:   repoURL.Host,
		Path:   repoURL.Path,
	}
	var queryParams []string
	if p.ref != "" {
		queryParams = append(queryParams, "ref="+p.ref)
	}
	if p.subpath != "" {
		queryParams = append(queryParams, "subpath="+p.subpath)
	}
	if len(queryParams) > 0 {
		gitURL.RawQuery = strings.Join(queryParams, "&")
	}
	return gitURL.String(), nil
}

func loadEnvCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Environment, *cobra.Command, []string) error,
	presetSecretToken string,
	envIsOptional bool,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		return withEngineAndTUI(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.RecorderFromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			load := vtx.Task("loading environment")
			loadedEnv, err := loadEnv(ctx, engineClient.Dagger(), envIsOptional)
			load.Done(err)
			if err != nil {
				return err
			}

			if !envIsOptional && loadedEnv == nil {
				return fmt.Errorf("no environment specified and no default environment found in current directory")
			}

			return fn(ctx, engineClient, loadedEnv, cmd, cmdArgs)
		})
	}
}

func loadEnv(ctx context.Context, c *dagger.Client, envIsOptional bool) (*dagger.Environment, error) {
	env, err := getEnvironmentFlagConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment config: %w", err)
	}
	envExists, err := env.envExists(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to check if environment exists: %w", err)
	}
	if !envExists {
		if envIsOptional {
			return nil, nil
		}
		return nil, fmt.Errorf("environment does not exist")
	}

	loadedEnv, err := env.load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment: %w", err)
	}

	// TODO: hack to unlazy env so it's actually loaded
	_, err = loadedEnv.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get loaded environment ID: %w", err)
	}

	return loadedEnv, nil
}
