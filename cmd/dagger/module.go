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
	moduleURL   string
	moduleFlags = pflag.NewFlagSet("module", pflag.ContinueOnError)

	sdk        string
	moduleName string
	moduleRoot string
)

const (
	moduleURLDefault = "."
)

func init() {
	moduleFlags.StringVarP(&moduleURL, "mod", "m", moduleURLDefault, "Path to dagger.json config file for the module or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger?ref=branch?subpath=path/to/some/dir\").")
	moduleFlags.BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	moduleCmd.PersistentFlags().AddFlagSet(moduleFlags)
	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)

	moduleInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK to use for the module")
	moduleInitCmd.MarkFlagRequired("sdk")
	moduleInitCmd.PersistentFlags().StringVar(&moduleName, "name", "", "Name of the new module")
	moduleInitCmd.MarkFlagRequired("name")
	moduleInitCmd.PersistentFlags().StringVarP(&moduleRoot, "root", "", "", "Root directory that should be loaded for the full module context. Defaults to the parent directory containing dagger.json.")
	// also include codegen flags since codegen will run on module init

	moduleCmd.AddCommand(moduleInitCmd)
	moduleCmd.AddCommand(moduleExtendCmd)
	moduleCmd.AddCommand(moduleSyncCmd)
}

var moduleCmd = &cobra.Command{
	Use:     "module",
	Aliases: []string{"mod"},
	Short:   "Manage dagger modules",
	Long:    "Manage dagger modules. By default, print the configuration of the specified module in json format.",
	Hidden:  true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		mod, err := getModuleFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get module: %w", err)
		}
		var cfg *moduleconfig.Config
		switch {
		case mod.local != nil:
			cfg, err = mod.local.config()
			if err != nil {
				return fmt.Errorf("failed to get local module config: %w", err)
			}
		case mod.git != nil:
			// we need to read the git repo, which currently requires an engine+client
			err = withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
				rec := progrock.FromContext(ctx)
				vtx := rec.Vertex("get-mod-config", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()
				readConfigTask := vtx.Task("reading git module config")
				cfg, err = mod.git.config(ctx, engineClient.Dagger())
				readConfigTask.Done(err)
				if err != nil {
					return fmt.Errorf("failed to get git module config: %w", err)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal module config: %w", err)
		}
		cmd.Println(string(cfgBytes))
		return nil
	},
}

var moduleInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger module in a local directory.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		ctx := cmd.Context()

		mod, err := getModuleFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get module: %w", err)
		}
		if mod != nil {
			if mod.git != nil {
				return fmt.Errorf("module init is not supported for git modules")
			}
			if _, err := os.Stat(mod.local.path); err == nil {
				return fmt.Errorf("module init config path already exists: %s", mod.local.path)
			}
		}

		cfg := &moduleconfig.Config{
			Name: moduleName,
			SDK:  moduleconfig.SDK(sdk),
			Root: moduleRoot,
		}

		return updateModuleConfig(ctx, mod.local.path, cfg, cmd)
	},
}

var moduleExtendCmd = &cobra.Command{
	Use:    "extend",
	Short:  "Extend a dagger module with access to the entrypoints of another module",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		modFlagCfg, err := getModuleFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get module: %w", err)
		}
		if modFlagCfg.git != nil {
			return fmt.Errorf("module extend is not supported for git modules")
		}
		modCfg, err := modFlagCfg.config(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get module config: %w", err)
		}

		depSet := make(map[string]struct{})
		for _, dep := range modCfg.Dependencies {
			depSet[dep] = struct{}{}
		}
		for _, newDep := range extraArgs {
			depModFlagCfg, err := getModuleFlagConfigFromURL(newDep)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			switch {
			case depModFlagCfg.local != nil:
				depPath, err := filepath.Rel(filepath.Dir(modFlagCfg.local.path), filepath.Dir(depModFlagCfg.local.path))
				if err != nil {
					return fmt.Errorf("failed to get relative path for dependency: %w", err)
				}
				depSet[depPath] = struct{}{}
			case depModFlagCfg.git != nil:
				gitURL, err := depModFlagCfg.git.urlString()
				if err != nil {
					return fmt.Errorf("failed to get git url for dependency: %w", err)
				}
				depSet[gitURL] = struct{}{}
			}
		}

		modCfg.Dependencies = nil
		for dep := range depSet {
			modCfg.Dependencies = append(modCfg.Dependencies, dep)
		}
		sort.Strings(modCfg.Dependencies)

		return updateModuleConfig(ctx, modFlagCfg.local.path, modCfg, cmd)
	},
}

var moduleSyncCmd = &cobra.Command{
	Use:    "sync",
	Short:  "Synchronize a dagger module with the latest version of its extensions",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		modFlagCfg, err := getModuleFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get module: %w", err)
		}
		if modFlagCfg.git != nil {
			return fmt.Errorf("module sync is not supported for git modules")
		}
		modCfg, err := modFlagCfg.config(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get module config: %w", err)
		}
		return updateModuleConfig(ctx, modFlagCfg.local.path, modCfg, cmd)
	},
}

func updateModuleConfig(
	ctx context.Context,
	path string,
	newModCfg *moduleconfig.Config,
	cmd *cobra.Command,
) (rerr error) {
	runCodegenFunc := func() error {
		return nil
	}
	switch newModCfg.SDK {
	case moduleconfig.SDKGo:
		runCodegenFunc = func() error {
			return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
				rec := progrock.FromContext(ctx)
				vtx := rec.Vertex("mod-update", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()

				loadDeps := vtx.Task("loading module dependencies")
				modFlagCfg := &moduleFlagConfig{local: &localModule{path: path}}
				deps, err := modFlagCfg.loadDeps(ctx, engineClient.Dagger())
				loadDeps.Done(err)
				if err != nil {
					return fmt.Errorf("failed to load dependencies: %w", err)
				}
				runCodegenTask := vtx.Task("generating module code")
				err = RunCodegen(ctx, engineClient, nil, newModCfg, deps, cmd, nil)
				runCodegenTask.Done(err)
				return err
			})
		}
	case moduleconfig.SDKPython:
	default:
		return fmt.Errorf("unsupported module SDK: %s", sdk)
	}

	cfgBytes, err := json.MarshalIndent(newModCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal module config: %w", err)
	}
	parentDir := filepath.Dir(path)
	_, parentDirStatErr := os.Stat(parentDir)
	switch {
	case parentDirStatErr == nil:
		// already exists, nothing to do
	case os.IsNotExist(parentDirStatErr):
		// make the parent dir, but if something goes wrong, clean it up in the defer
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create module config directory: %w", err)
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
			return fmt.Errorf("failed to stat module config: %w", err)
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
		return fmt.Errorf("failed to read module config: %w", configFileReadErr)
	}

	// nolint:gosec
	if err := os.WriteFile(path, append(cfgBytes, '\n'), cfgFileMode); err != nil {
		return fmt.Errorf("failed to write module config: %w", err)
	}

	if err := runCodegenFunc(); err != nil {
		return fmt.Errorf("failed to run codegen: %w", err)
	}

	return nil
}

func getModuleFlagConfig() (*moduleFlagConfig, error) {
	moduleURL := moduleURL
	if moduleURL == "" || moduleURL == moduleURLDefault {
		// it's unset or default value, use mod if present
		if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
			moduleURL = v
		}
	}
	return getModuleFlagConfigFromURL(moduleURL)
}

func getModuleFlagConfigFromURL(moduleURL string) (*moduleFlagConfig, error) {
	parsedURL, err := moduleconfig.ParseModuleURL(moduleURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module URL: %w", err)
	}
	switch {
	case parsedURL.Local != nil:
		localPath, err := filepath.Abs(parsedURL.Local.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get module absolute path: %w", err)
		}
		return &moduleFlagConfig{local: &localModule{
			path: localPath,
		}}, nil
	case parsedURL.Git != nil:
		return &moduleFlagConfig{git: &gitModule{
			repo:    parsedURL.Git.Repo,
			ref:     parsedURL.Git.Ref,
			subpath: parsedURL.Git.ConfigPath,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported module URL: %q", moduleURL)
	}
}

// moduleFlagConfig holds the module settings provided by the user via flags (or defaults if not set)
type moduleFlagConfig struct {
	// only one of these will be set
	local *localModule
	git   *gitModule
}

func (p moduleFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	var mod *dagger.Module
	var err error
	switch {
	case p.local != nil:
		mod, err = p.local.load(c)
	case p.git != nil:
		mod, err = p.git.load(ctx, c)
	default:
		return nil, fmt.Errorf("invalid module")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load module: %w", err)
	}
	// install the mod schema too
	if _, err := mod.Serve(ctx); err != nil {
		return nil, fmt.Errorf("failed to install module: %w", err)
	}
	return mod, nil
}

func (p moduleFlagConfig) config(ctx context.Context, c *dagger.Client) (*moduleconfig.Config, error) {
	switch {
	case p.local != nil:
		return p.local.config()
	case p.git != nil:
		return p.git.config(ctx, c)
	default:
		panic("invalid module")
	}
}

func (p moduleFlagConfig) loadDeps(ctx context.Context, c *dagger.Client) ([]*dagger.Module, error) {
	if p.local == nil {
		return nil, fmt.Errorf("TODO: implement non-local module dependency loading")
	}

	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	depMods := make([]*dagger.Module, 0, len(cfg.Dependencies))
	for _, dep := range cfg.Dependencies {
		depPath := filepath.Join(filepath.Dir(p.local.path), dep)
		if filepath.Base(depPath) != "dagger.json" {
			depPath = filepath.Join(depPath, "dagger.json")
		}
		depMod, err := localModule{path: depPath}.load(c)
		if err != nil {
			return nil, fmt.Errorf("failed to load dependency module: %w", err)
		}
		depMods = append(depMods, depMod)
	}

	var eg errgroup.Group
	for _, depMod := range depMods {
		depMod := depMod
		eg.Go(func() error {
			_, err = depMod.Serve(ctx)
			if err != nil {
				return fmt.Errorf("failed to serve dependency module %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return depMods, nil
}

func (p moduleFlagConfig) modExists(ctx context.Context, c *dagger.Client) (bool, error) {
	switch {
	case p.local != nil:
		return p.local.modExists()
	case p.git != nil:
		return p.git.modExists(ctx, c)
	default:
		return false, fmt.Errorf("invalid module")
	}
}

type localModule struct {
	path string
}

func (p localModule) config() (*moduleconfig.Config, error) {
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

func (p localModule) load(c *dagger.Client) (*dagger.Module, error) {
	rootDir, err := p.rootDir()
	if err != nil {
		return nil, err
	}
	subdirRelPath, err := filepath.Rel(rootDir, p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
	}

	if strings.HasPrefix(subdirRelPath, "..") {
		return nil, fmt.Errorf("module config path %q is not under module root %q", p.path, rootDir)
	}
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	hostDir := c.Host().Directory(rootDir, dagger.HostDirectoryOpts{
		Include: cfg.Include,
		Exclude: cfg.Exclude,
	})
	return hostDir.AsModule(dagger.DirectoryAsModuleOpts{
		SourceSubpath: subdirRelPath,
	}), nil
}

func (p localModule) rootDir() (string, error) {
	cfg, err := p.config()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(p.path), cfg.Root)), nil
}

func (p localModule) modExists() (bool, error) {
	_, err := os.Stat(p.path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to stat module config: %w", err)
}

type gitModule struct {
	subpath string
	repo    string
	ref     string
}

func (p gitModule) config(ctx context.Context, c *dagger.Client) (*moduleconfig.Config, error) {
	if c == nil {
		return nil, fmt.Errorf("cannot load git module config with nil dagger client")
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

func (p gitModule) load(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
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
		return nil, fmt.Errorf("module config path %q is not under module root %q", p.subpath, rootPath)
	}
	return c.Git(p.repo).Branch(p.ref).Tree().Directory(rootPath).AsModule(dagger.DirectoryAsModuleOpts{
		SourceSubpath: subdirRelPath,
	}), nil
}

func (p gitModule) modExists(ctx context.Context, c *dagger.Client) (bool, error) {
	_, err := c.Git(p.repo).Branch(p.ref).Tree().File(p.subpath).Sync(ctx)
	// TODO: this could technically fail for other reasons, but is okay enough for now, it will
	// still fail later if something else went wrong
	return err == nil, nil
}

// convert back to url string (with normalization after previous parsing)
func (p gitModule) urlString() (string, error) {
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

func loadModCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Module, *cobra.Command, []string) error,
	presetSecretToken string,
	modIsOptional bool,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		return withEngineAndTUI(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.FromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			load := vtx.Task("loading module")
			loadedMod, err := loadMod(ctx, engineClient.Dagger(), modIsOptional)
			load.Done(err)
			if err != nil {
				return err
			}

			if !modIsOptional && loadedMod == nil {
				return fmt.Errorf("no module specified and no default module found in current directory")
			}

			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}

func loadMod(ctx context.Context, c *dagger.Client, modIsOptional bool) (*dagger.Module, error) {
	mod, err := getModuleFlagConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}
	modExists, err := mod.modExists(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to check if module exists: %w", err)
	}
	if !modExists {
		if modIsOptional {
			return nil, nil
		}
		return nil, fmt.Errorf("module does not exist")
	}

	loadedMod, err := mod.load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load module: %w", err)
	}

	// TODO: hack to unlazy mod so it's actually loaded
	// TODO: is this still needed?
	_, err = loadedMod.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get loaded module ID: %w", err)
	}

	return loadedMod, nil
}
