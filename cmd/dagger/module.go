package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/moduleconfig"
	"github.com/dagger/dagger/core/resolver"
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
	moduleCmd.AddCommand(moduleUseCmd)
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

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			mod, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			var cfg *moduleconfig.Config
			switch {
			case mod.Local:
				cfg, err = mod.config(ctx, nil)
				if err != nil {
					return fmt.Errorf("failed to get local module config: %w", err)
				}
			case mod.Git != nil:
				rec := progrock.FromContext(ctx)
				vtx := rec.Vertex("get-mod-config", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()
				readConfigTask := vtx.Task("reading git module config")
				cfg, err = mod.config(ctx, engineClient.Dagger())
				readConfigTask.Done(err)
				if err != nil {
					return fmt.Errorf("failed to get git module config: %w", err)
				}
				return nil
			}
			cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal module config: %w", err)
			}
			cmd.Println(string(cfgBytes))
			return nil
		})
	},
}

var moduleInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger module in a local directory.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			mod, err := getModuleFlagConfig(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}

			if mod.Git != nil {
				return fmt.Errorf("module init is not supported for git modules")
			}

			if exists, err := mod.modExists(ctx, nil); err == nil && exists {
				return fmt.Errorf("module init config path already exists: %s", mod.Path)
			}

			cfg := &moduleconfig.Config{
				Name: moduleName,
				SDK:  moduleconfig.SDK(sdk),
				Root: moduleRoot,
			}

			return updateModuleConfig(ctx, engineClient, mod.Path, cfg, cmd)
		})
	},
}

var moduleUseCmd = &cobra.Command{
	Use:    "use",
	Short:  "Add a new dependency to a dagger module",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			modFlagCfg, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			if modFlagCfg.Git != nil {
				return fmt.Errorf("module use is not supported for git modules")
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
				depMod, err := resolver.ResolveModuleDependency(ctx, engineClient.Dagger(), modFlagCfg.Module, newDep)
				if err != nil {
					return fmt.Errorf("failed to get module: %w", err)
				}
				depSet[depMod.String()] = struct{}{}
			}

			modCfg.Dependencies = nil
			for dep := range depSet {
				modCfg.Dependencies = append(modCfg.Dependencies, dep)
			}
			sort.Strings(modCfg.Dependencies)

			return updateModuleConfig(ctx, engineClient, modFlagCfg.Path, modCfg, cmd)
		})
	},
}

var moduleSyncCmd = &cobra.Command{
	Use:    "sync",
	Short:  "Synchronize a dagger module with the latest version of its extensions",
	Hidden: true,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			modFlagCfg, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			if modFlagCfg.Git != nil {
				return fmt.Errorf("module sync is not supported for git modules")
			}
			modCfg, err := modFlagCfg.config(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed to get module config: %w", err)
			}
			return updateModuleConfig(ctx, engineClient, modFlagCfg.Path, modCfg, cmd)
		})
	},
}

func updateModuleConfig(
	ctx context.Context,
	engineClient *client.Client,
	moduleDir string,
	newModCfg *moduleconfig.Config,
	cmd *cobra.Command,
) (rerr error) {
	runCodegenFunc := func() error {
		return nil
	}
	switch newModCfg.SDK {
	case moduleconfig.SDKGo:
		runCodegenFunc = func() (err error) {
			rec := progrock.FromContext(ctx)
			vtx := rec.Vertex("mod-update", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			loadDeps := vtx.Task("loading module dependencies")
			mod, err := resolver.ResolveStableRef(moduleDir)
			if err != nil {
				return fmt.Errorf("failed to resolve module: %w", err)
			}
			deps, err := moduleFlagConfig{mod}.loadDeps(ctx, engineClient.Dagger())
			loadDeps.Done(err)
			if err != nil {
				return fmt.Errorf("failed to load dependencies: %w", err)
			}
			runCodegenTask := vtx.Task("generating module code")
			err = RunCodegen(ctx, engineClient, nil, newModCfg, deps, cmd, nil)
			runCodegenTask.Done(err)
			return err
		}
	case moduleconfig.SDKPython:
	default:
		return fmt.Errorf("unsupported module SDK: %s", sdk)
	}

	configPath := filepath.Join(moduleDir, moduleconfig.Filename)

	cfgBytes, err := json.MarshalIndent(newModCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal module config: %w", err)
	}
	_, parentDirStatErr := os.Stat(moduleDir)
	switch {
	case parentDirStatErr == nil:
		// already exists, nothing to do
	case os.IsNotExist(parentDirStatErr):
		// make the parent dir, but if something goes wrong, clean it up in the defer
		if err := os.MkdirAll(moduleDir, 0755); err != nil {
			return fmt.Errorf("failed to create module config directory: %w", err)
		}
		defer func() {
			if rerr != nil {
				os.RemoveAll(moduleDir)
			}
		}()
	default:
		return fmt.Errorf("failed to stat parent directory: %w", parentDirStatErr)
	}

	var cfgFileMode os.FileMode = 0644
	originalContents, configFileReadErr := os.ReadFile(configPath)
	switch {
	case configFileReadErr == nil:
		// attempt to restore the original file if it already existed and something goes wrong
		stat, err := os.Stat(configPath)
		if err != nil {
			return fmt.Errorf("failed to stat module config: %w", err)
		}
		cfgFileMode = stat.Mode()
		defer func() {
			if rerr != nil {
				os.WriteFile(configPath, originalContents, cfgFileMode)
			}
		}()
	case os.IsNotExist(configFileReadErr):
		// remove it if it didn't exist already and something goes wrong
		defer func() {
			if rerr != nil {
				os.RemoveAll(configPath)
			}
		}()
	default:
		return fmt.Errorf("failed to read module config: %w", configFileReadErr)
	}

	// nolint:gosec
	if err := os.WriteFile(configPath, append(cfgBytes, '\n'), cfgFileMode); err != nil {
		return fmt.Errorf("failed to write module config: %w", err)
	}

	if err := runCodegenFunc(); err != nil {
		return fmt.Errorf("failed to run codegen: %w", err)
	}

	return nil
}

func getModuleFlagConfig(ctx context.Context, dag *dagger.Client) (*moduleFlagConfig, error) {
	moduleURL := moduleURL
	if moduleURL == "" || moduleURL == moduleURLDefault {
		// it's unset or default value, use mod if present
		if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
			moduleURL = v
		}
	}
	return getModuleFlagConfigFromURL(ctx, dag, moduleURL)
}

func getModuleFlagConfigFromURL(ctx context.Context, dag *dagger.Client, moduleURL string) (*moduleFlagConfig, error) {
	modRef, err := resolver.ResolveMovingRef(ctx, dag, moduleURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module URL: %w", err)
	}
	return &moduleFlagConfig{modRef}, nil
}

// moduleFlagConfig holds the module settings provided by the user via flags (or defaults if not set)
type moduleFlagConfig struct {
	// only one of these will be set
	*resolver.Module
}

func (p moduleFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, err
	}

	var mod *dagger.Module
	switch {
	case p.Local:
		rootDir := filepath.Clean(filepath.Join(p.Path, cfg.Root))
		subdirRelPath, err := filepath.Rel(rootDir, p.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
		}
		if strings.HasPrefix(subdirRelPath, "..") {
			return nil, fmt.Errorf("module config path %q is not under module root %q", p.Path, rootDir)
		}

		mod = c.Host().Directory(rootDir, dagger.HostDirectoryOpts{
			Include: cfg.Include,
			Exclude: cfg.Exclude,
		}).AsModule(dagger.DirectoryAsModuleOpts{
			SourceSubpath: subdirRelPath,
		})

	case p.Git != nil:
		rootPath := path.Clean(path.Join(p.SubPath, cfg.Root))
		if strings.HasPrefix(rootPath, "..") {
			return nil, fmt.Errorf("module config path %q is not under module root %q", p.SubPath, rootPath)
		}

		mod = c.Git(p.Git.CloneURL).Commit(p.Version).Tree().
			Directory(rootPath).
			AsModule()

	default:
		err = fmt.Errorf("invalid module")
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
	case p.Local:
		configBytes, err := os.ReadFile(path.Join(p.Path, moduleconfig.Filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read local config file: %w", err)
		}
		var cfg moduleconfig.Config
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse local config file: %w", err)
		}
		return &cfg, nil

	case p.Git != nil:
		if c == nil {
			return nil, fmt.Errorf("cannot load git module config with nil dagger client")
		}
		repoDir := c.Git(p.Git.CloneURL).Commit(p.Version).Tree()
		var configPath string
		if p.SubPath != "" {
			configPath = path.Join(p.SubPath, moduleconfig.Filename)
		} else {
			configPath = moduleconfig.Filename
		}
		configStr, err := repoDir.File(configPath).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read git config file: %w", err)
		}
		var cfg moduleconfig.Config
		if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse git config file: %w", err)
		}
		return &cfg, nil

	default:
		panic("invalid module ref")
	}
}

func (p moduleFlagConfig) loadDeps(ctx context.Context, c *dagger.Client) ([]*dagger.Module, error) {
	if !p.Local {
		return nil, fmt.Errorf("TODO: implement non-local module dependency loading")
	}

	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	depMods := make([]*dagger.Module, 0, len(cfg.Dependencies))
	for _, dep := range cfg.Dependencies {
		depModFlagCfg, err := getModuleFlagConfigFromURL(ctx, c, dep)
		if err != nil {
			return nil, fmt.Errorf("failed to get module: %w", err)
		}
		depMod, err := depModFlagCfg.load(ctx, c)
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
	case p.Local:
		configPath := moduleconfig.NormalizeConfigPath(p.Path)
		_, err := os.Stat(configPath)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat module config: %w", err)
	case p.Git != nil:
		configPath := moduleconfig.NormalizeConfigPath(p.SubPath)
		_, err := c.Git(p.Git.CloneURL).Commit(p.Version).Tree().File(configPath).Sync(ctx)
		// TODO: this could technically fail for other reasons, but is okay enough for now, it will
		// still fail later if something else went wrong
		return err == nil, nil
	default:
		return false, fmt.Errorf("invalid module")
	}
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
	mod, err := getModuleFlagConfig(ctx, c)
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
