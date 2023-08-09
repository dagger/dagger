package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/environmentconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

var (
	environmentURI   string
	environmentFlags = pflag.NewFlagSet("environment", pflag.ContinueOnError)

	sdk             string
	environmentName string
	environmentRoot string
)

const (
	environmentURIDefault = "."
)

func init() {
	environmentFlags.StringVarP(&environmentURI, "env", "e", environmentURIDefault, "Path to dagger.json config file for the environment or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger?ref=branch?subpath=path/to/some/dir\").")
	environmentCmd.PersistentFlags().AddFlagSet(environmentFlags)
	doCmd.PersistentFlags().AddFlagSet(environmentFlags)
	checkCmd.PersistentFlags().AddFlagSet(environmentFlags)
	shellCmd.PersistentFlags().AddFlagSet(environmentFlags)
	listenCmd.PersistentFlags().AddFlagSet(environmentFlags)
	queryCmd.PersistentFlags().AddFlagSet(environmentFlags)
	codegenCmd.PersistentFlags().AddFlagSet(environmentFlags)

	environmentInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK to use for the environment")
	environmentInitCmd.MarkFlagRequired("sdk")
	environmentInitCmd.PersistentFlags().StringVar(&environmentName, "name", "", "Name of the new environment")
	environmentInitCmd.MarkFlagRequired("name")
	environmentInitCmd.PersistentFlags().StringVarP(&environmentRoot, "root", "", "", "Root directory that should be loaded for the full environment context. Defaults to the parent directory containing dagger.json.")

	environmentCmd.AddCommand(environmentInitCmd)
}

var environmentCmd = &cobra.Command{
	Use:    "environment",
	Short:  "Manage dagger environments",
	Long:   "Manage dagger environments. By default, print the configuration of the specified environment in json format.",
	Hidden: true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		env, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		var cfg *environmentconfig.Config
		switch {
		case env.local != nil:
			cfg, err = env.local.config()
			if err != nil {
				return fmt.Errorf("failed to get local environment config: %w", err)
			}
		case env.git != nil:
			// we need to read the git repo, which currently requires an engine+client
			err = withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, sess *client.Client) error {
				c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(sess)))
				if err != nil {
					return fmt.Errorf("failed to connect to dagger: %w", err)
				}
				defer c.Close()
				cfg, err = env.git.config(ctx, c)
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
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := getEnvironmentFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get environment: %w", err)
		}
		if env.git != nil {
			return fmt.Errorf("environment init is not supported for git environments")
		}

		if _, err := os.Stat(env.local.path); err == nil {
			return fmt.Errorf("environment init config path already exists: %s", env.local.path)
		}
		switch environmentconfig.SDK(sdk) {
		case environmentconfig.SDKGo, environmentconfig.SDKPython:
		default:
			return fmt.Errorf("unsupported environment SDK: %s", sdk)
		}
		cfg := &environmentconfig.Config{
			Name: environmentName,
			SDK:  environmentconfig.SDK(sdk),
			Root: environmentRoot,
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal environment config: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(env.local.path), 0755); err != nil {
			return fmt.Errorf("failed to create environment config directory: %w", err)
		}
		// nolint:gosec
		if err := os.WriteFile(env.local.path, cfgBytes, 0644); err != nil {
			return fmt.Errorf("failed to write environment config: %w", err)
		}
		return nil
	},
}

func getEnvironmentFlagConfig() (*environmentFlagConfig, error) {
	environmentURI := environmentURI
	if environmentURI == "" || environmentURI == environmentURIDefault {
		// it's unset or default value, use env if present
		if v, ok := os.LookupEnv("DAGGER_PROJECT"); ok {
			environmentURI = v
		}
	}

	url, err := url.Parse(environmentURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "", "local": // local path
		envPath, err := filepath.Abs(url.Host + url.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get environment absolute path: %w", err)
		}

		if filepath.Base(envPath) != "dagger.json" {
			envPath = filepath.Join(envPath, "dagger.json")
		}

		return &environmentFlagConfig{local: &localEnvironment{
			path: envPath,
		}}, nil
	case "git":
		repo := url.Host + url.Path

		// options for git environments are set via query params
		subpath := url.Query().Get("subpath")
		if path.Base(subpath) != "dagger.json" {
			subpath = path.Join(subpath, "dagger.json")
		}

		gitRef := url.Query().Get("ref")
		if gitRef == "" {
			gitRef = "main"
		}

		gitProtocol := url.Query().Get("protocol")
		if gitProtocol != "" {
			repo = gitProtocol + "://" + repo
		}

		p := &gitEnvironment{
			subpath: subpath,
			repo:    repo,
			ref:     gitRef,
		}
		return &environmentFlagConfig{git: p}, nil
	default:
		return nil, fmt.Errorf("unsupported environment URI scheme: %s", url.Scheme)
	}
}

// environmentFlagConfig holds the environment settings provided by the user via flags (or defaults if not set)
type environmentFlagConfig struct {
	// only one of these will be set
	local *localEnvironment
	git   *gitEnvironment
}

func (p environmentFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Environment, error) {
	switch {
	case p.local != nil:
		return p.local.load(c)
	case p.git != nil:
		return p.git.load(ctx, c)
	default:
		panic("invalid environment")
	}
}

func (p environmentFlagConfig) config(ctx context.Context, c *dagger.Client) (*environmentconfig.Config, error) {
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
	// TODO: hack to unlazy env load
	for _, depEnv := range depEnvs {
		depEnv := depEnv
		eg.Go(func() error {
			_, err := depEnv.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to load dependency environment %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return depEnvs, nil
}

type localEnvironment struct {
	path string
}

func (p localEnvironment) config() (*environmentconfig.Config, error) {
	configBytes, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read local config file: %w", err)
	}
	var cfg environmentconfig.Config
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
	return c.Environment().Load(hostDir, subdirRelPath), nil
}

func (p localEnvironment) rootDir() (string, error) {
	cfg, err := p.config()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(p.path), cfg.Root)), nil
}

type gitEnvironment struct {
	subpath string
	repo    string
	ref     string
}

func (p gitEnvironment) config(ctx context.Context, c *dagger.Client) (*environmentconfig.Config, error) {
	configStr, err := c.Git(p.repo).Branch(p.ref).Tree().File(p.subpath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read git config file: %w", err)
	}
	var cfg environmentconfig.Config
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
	return c.Environment().Load(c.Git(p.repo).Branch(p.ref).Tree().Directory(rootPath), subdirRelPath), nil
}

func loadEnvCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Client, *dagger.Environment, *cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
		flags.SetInterspersed(false) // stop parsing at first possible dynamic subcommand
		flags.AddFlagSet(cmd.Flags())
		flags.AddFlagSet(cmd.PersistentFlags())
		err := flags.Parse(args)
		if err != nil {
			return fmt.Errorf("failed to parse top-level flags: %w", err)
		}
		dynamicCmdArgs := flags.Args()

		focus = doFocus
		return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.RecorderFromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			connect := vtx.Task("connecting to Dagger")
			c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
			connect.Done(err)
			if err != nil {
				return fmt.Errorf("connect to dagger: %w", err)
			}

			load := vtx.Task("loading environment")
			loadedEnv, err := loadEnv(ctx, c)
			load.Done(err)
			if err != nil {
				return err
			}

			return fn(ctx, engineClient, c, loadedEnv, cmd, dynamicCmdArgs)
		})
	}
}

// TODO: dedupe w/ above where possible
func loadEnvDepsCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Client, *environmentconfig.Config, []*dagger.Environment, *cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
		flags.SetInterspersed(false) // stop parsing at first possible dynamic subcommand
		flags.AddFlagSet(cmd.Flags())
		flags.AddFlagSet(cmd.PersistentFlags())
		err := flags.Parse(args)
		if err != nil {
			return fmt.Errorf("failed to parse top-level flags: %w", err)
		}
		dynamicCmdArgs := flags.Args()

		focus = doFocus
		return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.RecorderFromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			connect := vtx.Task("connecting to Dagger")
			c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
			connect.Done(err)
			if err != nil {
				return fmt.Errorf("connect to dagger: %w", err)
			}

			load := vtx.Task("loading environment")
			envConfig, depEnvs, err := loadEnvDeps(ctx, c)
			load.Done(err)
			if err != nil {
				return err
			}

			return fn(ctx, engineClient, c, envConfig, depEnvs, cmd, dynamicCmdArgs)
		})
	}
}

func loadEnv(ctx context.Context, c *dagger.Client) (*dagger.Environment, error) {
	env, err := getEnvironmentFlagConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment config: %w", err)
	}
	if env.local != nil && outputPath == "" {
		// default to outputting to the environment root dir
		rootDir, err := env.local.rootDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get environment root dir: %w", err)
		}
		outputPath = rootDir
	}

	loadedEnv, err := env.load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment: %w", err)
	}

	// TODO: hack to unlazy env so it's actually loaded
	_, err = loadedEnv.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment ID: %w", err)
	}

	return loadedEnv, nil
}

func loadEnvDeps(ctx context.Context, c *dagger.Client) (*environmentconfig.Config, []*dagger.Environment, error) {
	env, err := getEnvironmentFlagConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get environment config: %w", err)
	}

	cfg, err := env.config(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	deps, err := env.loadDeps(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return cfg, deps, nil
}
