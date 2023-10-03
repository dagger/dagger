package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

var (
	moduleURL   string
	moduleFlags = pflag.NewFlagSet("module", pflag.ContinueOnError)

	sdk string

	moduleName string
	moduleRoot string
)

const (
	moduleURLDefault = "."
)

func init() {
	moduleFlags.StringVarP(&moduleURL, "mod", "m", "", "Path to dagger.json config file for the module or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger?ref=branch?subpath=path/to/some/dir\").")
	moduleFlags.BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	moduleCmd.PersistentFlags().AddFlagSet(moduleFlags)
	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)
	callCmd.PersistentFlags().AddFlagSet(moduleFlags)
	shellCmd.PersistentFlags().AddFlagSet(moduleFlags)

	moduleInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK name or image ref to use for the module")
	moduleInitCmd.MarkPersistentFlagRequired("sdk")
	moduleInitCmd.PersistentFlags().StringVar(&moduleName, "name", "", "Name of the new module")
	moduleInitCmd.MarkPersistentFlagRequired("name")
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
			mod, _, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			var cfg *modules.Config
			switch {
			case mod.Local:
				cfg, err = mod.Config(ctx, nil)
				if err != nil {
					return fmt.Errorf("failed to get local module config: %w", err)
				}
			case mod.Git != nil:
				rec := progrock.FromContext(ctx)
				vtx := rec.Vertex("get-mod-config", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()
				readConfigTask := vtx.Task("reading git module config")
				cfg, err = mod.Config(ctx, engineClient.Dagger())
				readConfigTask.Done(err)
				if err != nil {
					return fmt.Errorf("failed to get git module config: %w", err)
				}
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

			mod, _, err := getModuleFlagConfig(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}

			if mod.Git != nil {
				return fmt.Errorf("module init is not supported for git modules")
			}

			if exists, err := mod.modExists(ctx, nil); err == nil && exists {
				return fmt.Errorf("module init config path already exists: %s", mod.Path)
			}

			cfg := modules.NewConfig(moduleName, sdk, moduleRoot)
			return updateModuleConfig(ctx, engineClient, mod, cfg, cmd)
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
			modFlagCfg, _, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			if modFlagCfg.Git != nil {
				return fmt.Errorf("module use is not supported for git modules")
			}
			modCfg, err := modFlagCfg.Config(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module config: %w", err)
			}

			var deps []string
			deps = append(deps, modCfg.Dependencies...)
			deps = append(deps, extraArgs...)
			depSet := make(map[string]*modules.Ref)
			for _, dep := range deps {
				depMod, err := modules.ResolveModuleDependency(ctx, engineClient.Dagger(), modFlagCfg.Ref, dep)
				if err != nil {
					return fmt.Errorf("failed to get module: %w", err)
				}
				depSet[depMod.Path] = depMod
			}

			modCfg.Dependencies = nil
			for _, dep := range depSet {
				modCfg.Dependencies = append(modCfg.Dependencies, dep.String())
			}
			sort.Strings(modCfg.Dependencies)

			return updateModuleConfig(ctx, engineClient, modFlagCfg, modCfg, cmd)
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
			modFlagCfg, _, err := getModuleFlagConfig(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			if modFlagCfg.Git != nil {
				return fmt.Errorf("module sync is not supported for git modules")
			}
			modCfg, err := modFlagCfg.Config(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module config: %w", err)
			}
			return updateModuleConfig(ctx, engineClient, modFlagCfg, modCfg, cmd)
		})
	},
}

func updateModuleConfig(
	ctx context.Context,
	engineClient *client.Client,
	modFlag *moduleFlagConfig,
	modCfg *modules.Config,
	cmd *cobra.Command,
) (rerr error) {
	rec := progrock.FromContext(ctx)

	if !modFlag.Local {
		// nothing to do
		return nil
	}

	moduleDir, err := modFlag.LocalSourcePath()
	if err != nil {
		// TODO: impossible given Local check, would be nice to make unrepresentable
		return err
	}

	// pin dagger.json to the current runtime image version
	modCfg.SyncSDKRuntime()

	configPath := filepath.Join(moduleDir, modules.Filename)

	cfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
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

	dag := engineClient.Dagger()

	mod, err := modFlag.AsModule(ctx, dag)
	if err != nil {
		return fmt.Errorf("failed to load module: %w", err)
	}

	codegen := mod.GeneratedCode()

	if err := automateVCS(ctx, moduleDir, codegen); err != nil {
		return fmt.Errorf("failed to automate vcs: %w", err)
	}

	entries, err := codegen.Code().Entries(ctx)
	if err != nil {
		return fmt.Errorf("failed to get codegen output entries: %w", err)
	}

	rec.Debug("syncing generated files", progrock.Labelf("entries", "%v", entries))

	if _, err := codegen.Code().Export(ctx, moduleDir); err != nil {
		return fmt.Errorf("failed to export codegen output: %w", err)
	}

	return nil
}

func getModuleFlagConfig(ctx context.Context, dag *dagger.Client) (*moduleFlagConfig, bool, error) {
	wasSet := false

	moduleURL := moduleURL
	if moduleURL == "" {
		// it's unset or default value, use mod if present
		if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
			moduleURL = v
			wasSet = true
		}

		// it's still unset, set to the default
		if moduleURL == "" {
			moduleURL = moduleURLDefault
		}
	} else {
		wasSet = true
	}
	cfg, err := getModuleFlagConfigFromURL(ctx, dag, moduleURL)
	return cfg, wasSet, err
}

func getModuleFlagConfigFromURL(ctx context.Context, dag *dagger.Client, moduleURL string) (*moduleFlagConfig, error) {
	modRef, err := modules.ResolveMovingRef(ctx, dag, moduleURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module URL: %w", err)
	}
	return &moduleFlagConfig{modRef}, nil
}

// moduleFlagConfig holds the module settings provided by the user via flags (or defaults if not set)
type moduleFlagConfig struct {
	*modules.Ref
}

func (p moduleFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	mod, err := p.AsModule(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load module: %w", err)
	}

	// NB(vito): do NOT Serve the dependency; that installs it to the 'global'
	// schema view! we only want dependencies served directly to the dependent
	// module.

	return mod, nil
}

func (p moduleFlagConfig) modExists(ctx context.Context, c *dagger.Client) (bool, error) {
	switch {
	case p.Local:
		configPath := modules.NormalizeConfigPath(p.Path)
		_, err := os.Stat(configPath)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat module config: %w", err)
	case p.Git != nil:
		configPath := modules.NormalizeConfigPath(p.SubPath)
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
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		return withEngineAndTUI(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.FromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			load := vtx.Task("loading module")
			loadedMod, err := loadMod(ctx, engineClient.Dagger())
			load.Done(err)
			if err != nil {
				return err
			}

			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}

func loadMod(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	mod, modRequired, err := getModuleFlagConfig(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	modExists, err := mod.modExists(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to check if module exists: %w", err)
	}
	if !modExists && !modRequired {
		// only allow failing to load the mod when it was explicitly requested
		// by the user
		return nil, nil
	}

	loadedMod, err := mod.load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load module: %w", err)
	}

	// TODO: hack to unlazy mod so it's actually loaded
	// TODO: is this still needed?
	// TODO(vito): this came up again, specifically because I wanted the
	// dependencies to be started and served before doing schema introspection
	// for codegen. still seems useful, OR we could somehow have schema
	// introspection block/synchronize on loading dependencies automatically
	// _, err = loadedMod.ID(ctx)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to get loaded module ID: %w", err)
	// }

	// TODO(vito): immediate follow-up: turns out what I want is to Serve here
	// but _not_ Serve for each dependency, since we don't want them all
	// installed into the same schema - transitive deps should not be included.
	_, err = loadedMod.Serve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get loaded module ID: %w", err)
	}

	return loadedMod, nil
}
