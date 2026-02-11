package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/parallel"
	"github.com/go-git/go-git/v5"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
)

var (
	moduleGroup = &cobra.Group{
		ID:    "module",
		Title: "Dagger Module Commands",
	}

	moduleURL         string
	moduleNoURL       bool
	allowedLLMModules []string

	sdk           string
	licenseID     string
	compatVersion string

	moduleName       string
	moduleSourcePath string
	moduleIncludes   []string

	installName string

	initBlueprint        string
	toolchainInstallName string

	developSDK        string
	developSourcePath string
	developRecursive  bool

	selfCalls   bool
	noSelfCalls bool

	force bool

	autoApply    bool
	eagerRuntime bool
)

const (
	moduleURLDefault = "."
)

var moduleCmd = &cobra.Command{
	Use:     "module",
	Short:   "Manage workspace modules",
	GroupID: moduleGroup.ID,
}

// if the source root path already has some files
// then use `srcRootPath/.dagger` for source
func inferSourcePathDir(srcRootPath string) (string, error) {
	list, err := os.ReadDir(srcRootPath)
	switch {
	case err == nil:
		for _, l := range list {
			if l.Name() == "dagger.json" {
				continue
			}

			// .dagger already exist, return that
			if l.Name() == ".dagger" {
				return ".dagger", nil
			}

			// ignore hidden files
			if strings.HasPrefix(l.Name(), ".") {
				continue
			}

			return ".dagger", nil
		}
	case os.IsNotExist(err):
	default:
		return "", err
	}

	return ".", nil
}

func getCompatVersion() string {
	if compatVersion == "skip" {
		return ""
	}
	return compatVersion
}

// moduleAddFlags adds common module-related flags to a command.
// If optional is true, it also adds the --no-mod flag and marks --mod and --no-mod as mutually exclusive.
func moduleAddFlags(cmd *cobra.Command, flags *pflag.FlagSet, optional bool) {
	flags.StringVarP(&moduleURL, "mod", "m", "", "Module reference to load, either a local path or a remote git repo (defaults to current directory)")
	if optional {
		flags.BoolVarP(&moduleNoURL, "no-mod", "M", false, "Don't automatically load a module (mutually exclusive with --mod)")
		cmd.MarkFlagsMutuallyExclusive("mod", "no-mod")
	}

	var defaultAllowLLM []string
	if allowLLMEnv := os.Getenv("DAGGER_ALLOW_LLM"); allowLLMEnv != "" {
		defaultAllowLLM = strings.Split(allowLLMEnv, ",")
	}
	flags.StringSliceVar(&allowedLLMModules, "allow-llm", defaultAllowLLM, "List of URLs of remote modules allowed to access LLM APIs, or 'all' to bypass restrictions for the entire session")

	// Add the eager module loading flag to disable lazy load on runtime.
	flags.BoolVar(&eagerRuntime, "eager-runtime", false, "load module runtime eagerly")
}

func init() {
	moduleAddFlags(callModCmd.Command(), callModCmd.Command().PersistentFlags(), true)

	moduleAddFlags(funcListCmd, funcListCmd.PersistentFlags(), false)
	moduleAddFlags(listenCmd, listenCmd.PersistentFlags(), true)
	moduleAddFlags(queryCmd, queryCmd.PersistentFlags(), true)

	moduleAddFlags(mcpCmd, mcpCmd.PersistentFlags(), true)

	moduleAddFlags(shellCmd, shellCmd.PersistentFlags(), true)
	shellAddFlags(shellCmd)
	moduleAddFlags(checksCmd, checksCmd.PersistentFlags(), false)
	moduleAddFlags(rootCmd, rootCmd.Flags(), true)
	shellAddFlags(rootCmd)

	// module management commands
	moduleAddFlags(configCmd, configCmd.PersistentFlags(), false)

	moduleInitCmd.Flags().StringVar(&sdk, "sdk", "", "Optionally install a Dagger SDK")
	moduleInitCmd.Flags().StringVar(&moduleName, "name", "", "Name of the new module (defaults to parent directory name)")
	moduleInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleInitCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleInitCmd.Flags().StringSliceVar(&moduleIncludes, "include", nil, "Paths to include when loading the module. Only needed when extra paths are required to build the module. They are expected to be relative to the directory containing the module's dagger.json file (the module source root).")
	moduleInitCmd.Flags().StringVar(&initBlueprint, "blueprint", "", "Reference another module as blueprint")
	moduleInitCmd.Flags().BoolVar(&selfCalls, "with-self-calls", false, "Enable self-calls capability for the module (experimental)")

	// dagger module init
	moduleCmd.AddCommand(moduleModInitCmd)
	moduleModInitCmd.Flags().StringVar(&sdk, "sdk", "", "SDK to use (go, python, typescript)")
	moduleModInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Source directory used by the installed SDK")
	moduleModInitCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleModInitCmd.Flags().StringSliceVar(&moduleIncludes, "include", nil, "Paths to include when loading the module")

	modulePublishCmd.Flags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean")
	modulePublishCmd.Flags().StringVarP(&moduleURL, "mod", "m", "", "Module reference to publish, remote git repo (defaults to current directory)")

	moduleUnInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleUnInstallCmd, moduleUnInstallCmd.Flags(), false)

	moduleUpdateCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleUpdateCmd, moduleUpdateCmd.Flags(), false)

	moduleDevelopCmd.Flags().StringVar(&developSDK, "sdk", "", "Install the given Dagger SDK. Can be builtin (go, python, typescript) or a module address")
	moduleDevelopCmd.Flags().StringVar(&developSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleDevelopCmd.Flags().BoolVarP(&developRecursive, "recursive", "r", false, "Develop recursively into local dependencies")
	moduleDevelopCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleDevelopCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleDevelopCmd.Flags().Lookup("compat").NoOptDefVal = "skip"
	moduleDevelopCmd.Flags().BoolVar(&selfCalls, "with-self-calls", false, "Enable self-calls capability for the module (experimental)")
	moduleDevelopCmd.Flags().BoolVar(&noSelfCalls, "without-self-calls", false, "Disable self-calls capability for the module")
	moduleAddFlags(moduleDevelopCmd, moduleDevelopCmd.Flags(), false)

	toolchainInstallCmd.Flags().StringVarP(&toolchainInstallName, "name", "n", "", "Name to use for the toolchain in the module. Defaults to the name of the toolchain being installed.")
	toolchainInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(toolchainInstallCmd, toolchainInstallCmd.Flags(), false)

	toolchainUpdateCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(toolchainUpdateCmd, toolchainUpdateCmd.Flags(), false)

	toolchainUninstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(toolchainUninstallCmd, toolchainUninstallCmd.Flags(), false)

	moduleAddFlags(toolchainListCmd, toolchainListCmd.Flags(), false)

	toolchainCmd.AddCommand(toolchainInstallCmd)
	toolchainCmd.AddCommand(toolchainUpdateCmd)
	toolchainCmd.AddCommand(toolchainUninstallCmd)
	toolchainCmd.AddCommand(toolchainListCmd)
}

var moduleInitCmd = &cobra.Command{
	Use:   "init [options] [path]",
	Short: "Initialize a new module",
	Long: `Initialize a new module at the given path.

This creates a dagger.json file at the specified directory, making it the root of the new module.

If --sdk is specified, the given SDK is installed in the module. You can do this later with "dagger develop".
If --blueprint is specified, the given blueprint is installed in the module.
`,
	Example: `
# Reference a remote module as blueprint
dagger init --blueprint=github.com/example/blueprint

# Reference a local module as blueprint
dagger init --blueprint=../my/blueprints/simple-webapp

# Implement a standalone module in Go
dagger init --sdk=go
`,
	GroupID: moduleGroup.ID,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			// default the module source root to the current working directory if it doesn't exist yet
			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}
			srcRootArg := cwd
			if len(extraArgs) > 0 {
				srcRootArg = extraArgs[0]
			}
			if filepath.IsAbs(srcRootArg) {
				srcRootArg, err = filepath.Rel(cwd, srcRootArg)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
			}

			modSrc := dag.ModuleSource(srcRootArg, dagger.ModuleSourceOpts{
				// Tell the engine to use the provided arg as the source root, don't
				// try to find-up a dagger.json in a parent directory and use that as
				// the source root.
				// This enables cases like initializing a new module in a subdirectory of
				// another existing module.
				DisableFindUp: true,
				// It's okay if the source root/source dir don't exist yet since we'll
				// create them when exporting the generated context directory.
				AllowNotExists: true,
				// We can only init local modules
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if alreadyExists {
				return fmt.Errorf("module already exists")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}
			srcRootSubPath, err := modSrc.SourceRootSubpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get source root subpath: %w", err)
			}
			srcRootAbsPath := filepath.Join(contextDirPath, srcRootSubPath)

			// default module name to directory of source root
			if moduleName == "" {
				moduleName = filepath.Base(srcRootAbsPath)
			}

			// only bother setting source path if there's an sdk at this time
			if sdk != "" {
				// if user didn't specify moduleSourcePath explicitly,
				// check if current dir is non-empty and infer the source
				// path accordingly.
				if moduleSourcePath == "" {
					moduleSourcePath, err = inferSourcePathDir(srcRootAbsPath)
					if err != nil {
						return err
					}
				} else {
					// ensure source path is relative to the source root
					sourceAbsPath, err := pathutil.Abs(moduleSourcePath)
					if err != nil {
						return fmt.Errorf("failed to get absolute source path for %s: %w", moduleSourcePath, err)
					}

					moduleSourcePath, err = filepath.Rel(srcRootAbsPath, sourceAbsPath)
					if err != nil {
						return fmt.Errorf("failed to get relative source path: %w", err)
					}
				}
			}

			modSrc = modSrc.WithName(moduleName)
			if sdk != "" {
				modSrc = modSrc.WithSDK(sdk)
			}
			if moduleSourcePath != "" {
				modSrc = modSrc.WithSourceSubpath(moduleSourcePath)
			}
			if len(moduleIncludes) > 0 {
				modSrc = modSrc.WithIncludes(moduleIncludes)
			}
			// engine version must be set before setting blueprint
			modSrc = modSrc.WithEngineVersion(modules.EngineVersionLatest)
			// Install blueprint if specified
			if initBlueprint != "" {
				// Validate that we don't have both SDK and blueprint
				if sdk != "" {
					return fmt.Errorf("cannot specify both --sdk and --blueprint; use one or the other")
				}
				// Create a new module source for the blueprint installation
				blueprintSrc := dag.ModuleSource(initBlueprint, dagger.ModuleSourceOpts{
					DisableFindUp: true,
				})
				// Install the blueprint
				modSrc = modSrc.WithBlueprint(blueprintSrc)
			}

			if selfCalls {
				if sdk == "" {
					return fmt.Errorf("cannot enable self-calls feature without specifying --sdk")
				}
				modSrc = modSrc.WithExperimentalFeatures([]dagger.ModuleSourceExperimentalFeature{dagger.ModuleSourceExperimentalFeatureSelfCalls})
			}

			// Export generated files, including dagger.json
			_, err = modSrc.GeneratedContextDirectory().Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			if sdk != "" {
				// If we're generating code by setting a SDK, we should also generate a license
				// if it doesn't already exists.
				searchExisting := !cmd.Flags().Lookup("license").Changed
				if err := findOrCreateLicense(ctx, srcRootAbsPath, searchExisting); err != nil {
					return err
				}
			}

			// Print success message to user
			infoMessage := []any{"Initialized module", moduleName, "in", srcRootAbsPath}
			if initBlueprint != "" {
				infoMessage = append(infoMessage, "with blueprint", initBlueprint)
			}
			fmt.Fprintln(cmd.OutOrStdout(), infoMessage...)
			return nil
		})
	},
}

// moduleModInitCmd is the workspace-aware "dagger module init" subcommand.
// It creates a new module inside the workspace at .dagger/modules/<name>/
// and auto-installs it in .dagger/config.toml.
var moduleModInitCmd = &cobra.Command{
	Use:   "init [options] --sdk=<sdk> <name>",
	Short: "Create a new module in the workspace",
	Long: `Create a new module in the workspace.

When inside a workspace, the module is created at .dagger/modules/<name>/ and
automatically added to .dagger/config.toml so it's immediately callable via
'dagger call <name> <function>'.

When no workspace is found, the module is created in the current directory.`,
	Example: `dagger module init --sdk=go ci
  dagger module init --sdk=python deploy`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		if sdk == "" {
			return fmt.Errorf("--sdk is required; specify the SDK to use (go, python, typescript)")
		}

		modName := extraArgs[0]

		return withEngine(ctx, client.Params{
			// Skip workspace loading — module init does its own CLI-side detection
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			// 1. Detect workspace
			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}
			ws, err := workspace.DetectLocal(cwd)
			if err != nil {
				return fmt.Errorf("failed to detect workspace: %w", err)
			}

			// 2. Determine module path
			// We're "in a workspace" if .dagger/ or .git exists at the workspace root
			// (vs. the cwd fallback when nothing was found)
			var srcRootArg string
			daggerDirExists := false
			if info, err := os.Stat(filepath.Join(ws.Root, workspace.WorkspaceDirName)); err == nil && info.IsDir() {
				daggerDirExists = true
			}
			gitExists := false
			if _, err := os.Stat(filepath.Join(ws.Root, ".git")); err == nil {
				gitExists = true
			}
			inWorkspace := daggerDirExists || gitExists
			if inWorkspace {
				// Inside workspace: create at .dagger/modules/<name>/
				srcRootArg = filepath.Join(ws.Root, workspace.WorkspaceDirName, "modules", modName)
			} else {
				// Standalone: create in current directory
				srcRootArg = filepath.Join(cwd, modName)
			}

			// Make relative for engine
			relPath, err := filepath.Rel(cwd, srcRootArg)
			if err != nil {
				return fmt.Errorf("failed to get relative path: %w", err)
			}

			// 3. Create module via engine (reuses existing init logic)
			modSrc := dag.ModuleSource(relPath, dagger.ModuleSourceOpts{
				DisableFindUp:  true,
				AllowNotExists: true,
				RequireKind:    dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return fmt.Errorf("failed to check if module already exists: %w", err)
			}
			if alreadyExists {
				return fmt.Errorf("module %q already exists at %s", modName, srcRootArg)
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get local context directory path: %w", err)
			}

			modSrc = modSrc.WithName(modName)
			modSrc = modSrc.WithSDK(sdk)

			if moduleSourcePath != "" {
				srcRootSubPath, err := modSrc.SourceRootSubpath(ctx)
				if err != nil {
					return fmt.Errorf("failed to get source root subpath: %w", err)
				}
				srcRootAbsPath := filepath.Join(contextDirPath, srcRootSubPath)
				sourceAbsPath, err := pathutil.Abs(moduleSourcePath)
				if err != nil {
					return fmt.Errorf("failed to get absolute source path for %s: %w", moduleSourcePath, err)
				}
				relSourcePath, err := filepath.Rel(srcRootAbsPath, sourceAbsPath)
				if err != nil {
					return fmt.Errorf("failed to get relative source path: %w", err)
				}
				modSrc = modSrc.WithSourceSubpath(relSourcePath)
			}

			if len(moduleIncludes) > 0 {
				modSrc = modSrc.WithIncludes(moduleIncludes)
			}

			modSrc = modSrc.WithEngineVersion(modules.EngineVersionLatest)

			// Export generated files (dagger.json + SDK scaffolding)
			_, err = modSrc.GeneratedContextDirectory().Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to generate module: %w", err)
			}

			// Generate license
			searchExisting := !cmd.Flags().Lookup("license").Changed
			if err := findOrCreateLicense(ctx, srcRootArg, searchExisting); err != nil {
				return err
			}

			// 4. Auto-install in workspace config
			if inWorkspace {
				cfg := ws.Config
				if cfg == nil {
					cfg = &workspace.Config{}
				}
				if cfg.Modules == nil {
					cfg.Modules = make(map[string]workspace.ModuleEntry)
				}

				// Source path relative to .dagger/ directory
				sourcePath := filepath.Join("modules", modName)
				cfg.Modules[modName] = workspace.ModuleEntry{Source: sourcePath}

				daggerDir := filepath.Join(ws.Root, workspace.WorkspaceDirName)
				if err := os.MkdirAll(daggerDir, 0o755); err != nil {
					return fmt.Errorf("failed to create %s: %w", daggerDir, err)
				}
				configPath := filepath.Join(daggerDir, workspace.ConfigFileName)
				if err := os.WriteFile(configPath, workspace.SerializeConfig(cfg), 0o644); err != nil {
					return fmt.Errorf("failed to write %s: %w", configPath, err)
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Created module %q at %s\n", modName, srcRootArg)
				fmt.Fprintf(cmd.OutOrStdout(), "Installed in %s\n", configPath)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Created module %q at %s\n", modName, srcRootArg)
			}

			return nil
		})
	},
}

var moduleUpdateCmd = &cobra.Command{
	Use:     "update [options] [<DEPENDENCY>...]",
	Aliases: []string{"use"},
	Short:   "Update a module's dependencies",
	Long: `Update the dependencies of a local module.

To update only specific dependencies, specify their short names or a complete address.

If no dependency is specified, all dependencies are updated, as well as the module's blueprint, if it exists.
`,
	Example: `"dagger update" or "dagger update hello" "dagger update github.com/shykes/daggerverse/hello@v0.3.0"`,
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only update dependencies on a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}

			// If no dependency is specified, also update the blueprint
			if len(extraArgs) == 0 {
				modSrc = modSrc.WithUpdateBlueprint()
			}
			modSrc = modSrc.WithUpdateDependencies(extraArgs)
			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to update dependencies: %w", err)
			}

			return nil
		})
	},
}

var moduleUnInstallCmd = &cobra.Command{
	Use:     "uninstall [options] <module>",
	Short:   "Uninstall a dependency",
	Long:    "Uninstall module as a dependency from the current module. The target module must be local.",
	Example: "dagger uninstall hello",
	GroupID: moduleGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only uninstall dependencies on a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}

			modSrc = modSrc.WithoutDependencies(extraArgs)
			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to update dependencies: %w", err)
			}

			return nil
		})
	},
}

var moduleDevelopCmd = &cobra.Command{
	Use:   "develop [options]",
	Short: "Prepare a local module for development",
	Long: `Ensure that a module's SDK is installed, configured, and all its files re-generated.

It has different uses in different contexts:

- In a module without SDK: install an SDK and start an implementation
- In a fresh checkout of a module repository: make sure IDE auto-complete is up-to-date
- In a module with local dependencies: re-generate bindings for all dependencies
- In a module after upgrading the engine: upgrade the target engine version, and check for breaking changes

This command is idempotent: you can run it at any time, any number of times. It will:

1. Ensure that an SDK is installed
2. Ensure that custom SDK configuration is applied
3. Update the target engine version if needed
4. Ensure that a module implementation exists, and create a starter template if not
5. Generate the latest client bindings for the Dagger API and installed dependencies
`,
	Args:    cobra.NoArgs,
	GroupID: moduleGroup.ID,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		if selfCalls && noSelfCalls {
			return fmt.Errorf("cannot use --with-self-calls and --without-self-calls at the same time")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only export updated generated files for a local modules
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			if selfCalls {
				modSrc = modSrc.WithExperimentalFeatures([]dagger.ModuleSourceExperimentalFeature{dagger.ModuleSourceExperimentalFeatureSelfCalls})
			} else if noSelfCalls {
				modSrc = modSrc.WithoutExperimentalFeatures([]dagger.ModuleSourceExperimentalFeature{dagger.ModuleSourceExperimentalFeatureSelfCalls})
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}
			srcRootSubPath, err := modSrc.SourceRootSubpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get source root subpath: %w", err)
			}
			baseSrcRootPath := filepath.Join(contextDirPath, srcRootSubPath)

			modSrcs := make(map[string]*dagger.ModuleSource)
			if developRecursive {
				ctx, span := Tracer().Start(ctx, "load module: "+modRef, telemetry.Encapsulate())
				err := collectLocalModulesRecursive(ctx, modSrc, modSrcs)
				telemetry.EndWithCause(span, &err)
				if err != nil {
					return err
				}
			} else {
				modSrcs[baseSrcRootPath] = modSrc
			}

			ctx, span := Tracer().Start(ctx, "develop")
			defer telemetry.EndWithCause(span, &err)

			eg, ctx := errgroup.WithContext(ctx)
			sem := semaphore.NewWeighted(int64(engineClient.NumCPU()))
			for srcRootPath, modSrc := range modSrcs {
				name := strings.TrimPrefix(srcRootPath, baseSrcRootPath)
				name = strings.TrimPrefix(name, "/")
				if name == "" {
					name = "."
				}
				ctx, span := Tracer().Start(ctx, "develop "+name, telemetry.Encapsulate())
				eg.Go(func() (err error) {
					if err := sem.Acquire(ctx, 1); err != nil {
						return err
					}
					defer sem.Release(1)
					defer telemetry.EndWithCause(span, &err)

					if engineVersion := getCompatVersion(); engineVersion != "" {
						modSrc = modSrc.WithEngineVersion(engineVersion)
					}

					modSDK, err := modSrc.SDK().Source(ctx)
					if err != nil {
						return fmt.Errorf("failed to get module SDK: %w", err)
					}
					if developSDK != "" {
						if modSDK != "" && modSDK != developSDK {
							return fmt.Errorf("cannot update module SDK that has already been set to %q", modSDK)
						}
						modSDK = developSDK
						modSrc = modSrc.WithSDK(modSDK)
					}

					modSourcePath, err := modSrc.SourceSubpath(ctx)
					if err != nil {
						return fmt.Errorf("failed to get module source subpath: %w", err)
					}
					// if SDK is set but source path isn't and the user didn't provide --source, we'll use the default source path
					if modSDK != "" && modSourcePath == "" && developSourcePath == "" {
						inferredSourcePath, err := inferSourcePathDir(srcRootPath)
						if err != nil {
							return err
						}

						developSourcePath = filepath.Join(srcRootPath, inferredSourcePath)
					}

					clients, err := modSrc.ConfigClients(ctx)
					if err != nil {
						return fmt.Errorf("failed to get module clients configuration: %w", err)
					}

					// if there's no SDK and the user isn't changing the source path, there's nothing to do.
					// error out rather than silently doing nothing.
					if modSDK == "" && developSourcePath == "" && len(clients) == 0 {
						return fmt.Errorf("dagger develop on a module without an SDK or clients requires either --sdk or --source")
					}

					if developSourcePath != "" {
						// ensure source path is relative to the source root
						sourceAbsPath, err := pathutil.Abs(developSourcePath)
						if err != nil {
							return fmt.Errorf("failed to get absolute source path for %s: %w", developSourcePath, err)
						}
						developSourcePath, err = filepath.Rel(srcRootPath, sourceAbsPath)
						if err != nil {
							return fmt.Errorf("failed to get relative source path: %w", err)
						}

						if modSourcePath != "" && modSourcePath != developSourcePath {
							return fmt.Errorf("cannot update module source path that has already been set to %q", modSourcePath)
						}

						modSourcePath = developSourcePath
						modSrc = modSrc.WithSourceSubpath(modSourcePath)
					}

					contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
					if err != nil {
						return localModuleErrorf("failed to get local context directory path: %w", err)
					}
					_, err = modSrc.
						GeneratedContextDirectory().
						Export(ctx, contextDirPath)
					if err != nil {
						return fmt.Errorf("failed to generate code: %w", err)
					}

					// If no license has been created yet, and SDK is set, we should create one.
					if developSDK != "" {
						searchExisting := !cmd.Flags().Lookup("license").Changed
						if err := findOrCreateLicense(ctx, srcRootPath, searchExisting); err != nil {
							return err
						}
					}
					return nil
				})
			}
			return eg.Wait()
		})
	},
}

var toolchainCmd = &cobra.Command{
	Use:   "toolchain",
	Short: "Manage toolchains",
	Annotations: map[string]string{
		"experimental": "true",
	},
}

var toolchainInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Short:   "Install a toolchain to the current module",
	Long:    "Install another module as a toolchain to the current module.",
	Example: "dagger toolchain install github.com/dagger/dagger/toolchains/go",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only install toolchains to a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}

			toolchainRefStr := extraArgs[0]
			toolchainSrc := dag.ModuleSource(toolchainRefStr, dagger.ModuleSourceOpts{
				DisableFindUp: true,
			})

			if toolchainInstallName != "" {
				toolchainSrc = toolchainSrc.WithName(toolchainInstallName)
			}

			modSrc = modSrc.WithToolchains([]*dagger.ModuleSource{toolchainSrc})

			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to install toolchain: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "toolchain installed\n")
			return nil
		})
	},
}

var toolchainUpdateCmd = &cobra.Command{
	Use:     "update [options] [<toolchain>...]",
	Short:   "Update toolchains",
	Long:    "Update all or specific toolchains of the current module.",
	Example: "dagger toolchain update",
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only update toolchains on a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}

			modSrc = modSrc.WithUpdateBlueprint()
			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to update toolchains: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "toolchains updated\n")
			return nil
		})
	},
}

var toolchainUninstallCmd = &cobra.Command{
	Use:     "uninstall [options] <toolchain>",
	Short:   "Uninstall a toolchain",
	Long:    "Uninstall a toolchain from the current module.",
	Example: "dagger toolchain uninstall mytoolchain",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only uninstall toolchains on a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}

			toolchainRefStr := extraArgs[0]

			modSrc = modSrc.WithoutToolchains(([]string{toolchainRefStr}))
			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to uninstall toolchain: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "toolchain uninstalled\n")
			return nil
		})
	},
}

func loadToolchainInfo(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource) ([]toolchainInfo, error) {
	var info []toolchainInfo
	err := parallel.Run(ctx, "fetch toolchain information", func(ctx context.Context) error {
		alreadyExists, err := modSrc.ConfigExists(ctx)
		if err != nil {
			return localModuleErrorf("failed to check if module already exists: %w", err)
		}
		if !alreadyExists {
			return fmt.Errorf("module must be fully initialized")
		}
		toolchains, err := modSrc.Toolchains(ctx)
		if err != nil {
			return fmt.Errorf("failed to get toolchains: %w", err)
		}
		if len(toolchains) == 0 {
			return nil
		}
		info = make([]toolchainInfo, len(toolchains))
		jobs := parallel.New().WithInternal(true).WithReveal(false)
		for i, toolchain := range toolchains {
			jobs = jobs.WithJob("", func(ctx context.Context) error {
				toolchainDef, err := inspectModule(ctx, dag, &toolchain)
				if err != nil {
					return fmt.Errorf("inspect toolchain: %w", err)
				}
				info[i].name = toolchainDef.Name
				info[i].description = toolchainDef.Description
				return nil
			})
		}
		return jobs.Run(ctx)
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

type toolchainInfo struct {
	name        string
	description string
}

var toolchainListCmd = &cobra.Command{
	Use:     "list [options]",
	Short:   "List all toolchains",
	Long:    "List all toolchains of the current module.",
	Example: "dagger toolchain list",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// We can only list toolchains from a local module
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})
			toolchains, err := loadToolchainInfo(ctx, dag, modSrc)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
			fmt.Fprintf(tw, "%s\t%s\n",
				termenv.String("Name").Bold(),
				termenv.String("Description").Bold(),
			)
			sort.Slice(toolchains, func(i, j int) bool {
				return toolchains[i].name < toolchains[j].name
			})
			for _, toolchain := range toolchains {
				fmt.Fprintf(tw, "%s\t%s\n",
					toolchain.name,
					shortDescription(toolchain.description))
			}
			return tw.Flush()
		})
	},
}

func collectLocalModulesRecursive(ctx context.Context, base *dagger.ModuleSource, m map[string]*dagger.ModuleSource) error {
	kind, err := base.Kind(ctx)
	if err != nil {
		return err
	}
	if kind != dagger.ModuleSourceKindLocalSource {
		return nil
	}

	contextDirPath, err := base.LocalContextDirectoryPath(ctx)
	if err != nil {
		return localModuleErrorf("failed to get local context directory path: %w", err)
	}
	srcRootSubPath, err := base.SourceRootSubpath(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source root subpath: %w", err)
	}
	srcRootAbsPath := filepath.Join(contextDirPath, srcRootSubPath)

	if _, ok := m[srcRootAbsPath]; ok {
		return nil // already collected
	}
	m[srcRootAbsPath] = base

	deps, err := base.Dependencies(ctx)
	if err != nil {
		return err
	}
	for _, dep := range deps {
		err := collectLocalModulesRecursive(ctx, &dep, m)
		if err != nil {
			return err
		}
	}
	return nil
}

const daDaggerverse = "https://daggerverse.dev"

var modulePublishCmd = &cobra.Command{
	Use:    "publish [options]",
	Hidden: true, // Hide while we finalize publishing workflow
	Short:  "Publish a Dagger module to the Daggerverse",
	Long: fmt.Sprintf(`Publish a local module to the Daggerverse (%s).

The module needs to be committed to a git repository and have a remote
configured with name "origin". The git repository must be clean (unless
forced), to avoid mistakenly depending on uncommitted files.
`,
		daDaggerverse,
	),
	GroupID: moduleGroup.ID,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			slog := slog.SpanLogger(ctx, InstrumentationLibrary)
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				// can only publish modules that also exist locally for now
				RequireKind: dagger.ModuleSourceKindLocalSource,
			})

			alreadyExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				return localModuleErrorf("failed to check if module already exists: %w", err)
			}
			if !alreadyExists {
				return fmt.Errorf("module must be fully initialized")
			}

			contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
			if err != nil {
				return localModuleErrorf("failed to get local context directory path: %w", err)
			}
			srcRootSubPath, err := modSrc.SourceRootSubpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get source root subpath: %w", err)
			}
			srcRootAbsPath := filepath.Join(contextDirPath, srcRootSubPath)

			repo, err := git.PlainOpenWithOptions(srcRootAbsPath, &git.PlainOpenOptions{
				DetectDotGit:          true,
				EnableDotGitCommonDir: true,
			})
			if err != nil {
				return fmt.Errorf("failed to open git repo: %w", err)
			}
			wt, err := repo.Worktree()
			if err != nil {
				return fmt.Errorf("failed to get git worktree: %w", err)
			}
			st, err := wt.Status()
			if err != nil {
				return fmt.Errorf("failed to get git status: %w", err)
			}
			head, err := repo.Head()
			if err != nil {
				return fmt.Errorf("failed to get git HEAD: %w", err)
			}
			commit := head.Hash()

			slog.Debug("git commit", "commit", commit.String())

			orig, err := repo.Remote("origin")
			if err != nil {
				return fmt.Errorf("failed to get git remote: %w", err)
			}
			refPath, err := originToPath(orig.Config().URLs[0])
			if err != nil {
				return fmt.Errorf("failed to get module path: %w", err)
			}

			// calculate path relative to repo root
			gitRoot := wt.Filesystem.Root()
			pathFromRoot, err := filepath.Rel(gitRoot, srcRootAbsPath)
			if err != nil {
				return fmt.Errorf("failed to get path from git root: %w", err)
			}

			// NB: you might think to ignore changes to files outside of the module,
			// but we should probably play it safe. in a monorepo for example this
			// could mean publishing a broken module because it depends on
			// uncommitted code in a dependent module.
			//
			// TODO: the proper fix here might be to check for dependent code, too.
			// Specifically I should be able to publish a dependency before
			// committing + pushing its dependers. but in the end it doesn't really
			// matter; just commit everything and _then_ publish.
			if !st.IsClean() && !force {
				cmd.Println(st)
				return fmt.Errorf("git repository is not clean; run with --force to ignore")
			}

			refStr := fmt.Sprintf("%s@%s", path.Join(refPath, pathFromRoot), commit)

			crawlURL, err := url.JoinPath(daDaggerverse, "crawl")
			if err != nil {
				return fmt.Errorf("failed to get module URL: %w", err)
			}

			data := url.Values{}
			data.Add("ref", refStr)
			req, err := http.NewRequest(http.MethodPut, crawlURL, strings.NewReader(data.Encode()))
			if err != nil {
				return fmt.Errorf("failed to create request: %w", err)
			}

			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}

			// TODO(vito): inspect response and/or poll, would be nice to surface errors here

			cmd.Println("Publishing", refStr, "to", daDaggerverse+"...")
			cmd.Println()
			cmd.Println("You can check on the crawling status here:")
			cmd.Println()
			cmd.Println("    " + res.Request.URL.String())

			modURL, err := url.JoinPath(daDaggerverse, "mod", refStr)
			if err != nil {
				return fmt.Errorf("failed to get module URL: %w", err)
			}
			cmd.Println()
			cmd.Println("Once the crawl is complete, you can view your module here:")
			cmd.Println()
			cmd.Println("    " + modURL)

			return res.Body.Close()
		})
	},
}

func originToPath(origin string) (string, error) {
	url, err := gitutil.ParseURL(origin)
	if err != nil {
		return "", fmt.Errorf("failed to parse git remote origin URL: %w", err)
	}
	return strings.TrimSuffix(path.Join(url.Host, url.Path), ".git"), nil
}

func getExplicitModuleSourceRef() (string, bool) {
	if moduleNoURL {
		return "", false
	}
	if moduleURL != "" {
		return moduleURL, true
	}

	// it's unset or default value, use mod if present
	if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
		return v, true
	}

	return "", false
}

func getModuleSourceRefWithDefault() (string, error) {
	if v, ok := getExplicitModuleSourceRef(); ok {
		return v, nil
	}
	if moduleNoURL {
		return "", fmt.Errorf("cannot use default module source with --no-mod")
	}
	return moduleURLDefault, nil
}

func localModuleErrorf(format string, err error) error {
	if err == nil {
		return nil
	}

	wrapped := fmt.Errorf(format, err)
	if moduleURL != "" {
		return fmt.Errorf("%w\nhint: module source came from --mod=%q; if you intended local, pass `--mod .`", wrapped, moduleURL)
	}
	if envRef, ok := os.LookupEnv("DAGGER_MODULE"); ok {
		return fmt.Errorf("%w\nhint: module source came from DAGGER_MODULE=%q; if you intended local, pass `--mod .`", wrapped, envRef)
	}
	return wrapped
}

// Wraps a command with optional module loading. If a module was explicitly specified by the user,
// it will try to load it and error out if it's not found or invalid. If no module was specified,
// it will try the current directory as a module but provide a nil module if it's not found, not
// erroring out.
func optionalModCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Module, *cobra.Command, []string) error,
	presetSecretToken string,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		return withEngine(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			_, explicitModRefSet := getExplicitModuleSourceRef()

			if disableHostRW {
				// we could never possibly load a module, don't even try
				if explicitModRefSet {
					return fmt.Errorf("cannot load module with --disable-host-read-write enabled")
				}
				return fn(ctx, engineClient, nil, cmd, cmdArgs)
			}
			if moduleNoURL {
				return fn(ctx, engineClient, nil, cmd, cmdArgs)
			}

			dag := engineClient.Dagger()
			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
				AllowNotExists: true,
			})
			configExists, err := modSrc.ConfigExists(ctx)
			if err != nil {
				if strings.Contains(err.Error(), "rpc error: code = Unimplemented desc") {
					// this is a very obscure corner case: when running `dagger listen --disable-host-read-write`
					// and then running `dagger query` against that listener, we will not have disableHostRW set
					// true but do need to ignore this error about filesync being disabled
					return fn(ctx, engineClient, nil, cmd, cmdArgs)
				}
				return fmt.Errorf("failed to check if module exists: %w", err)
			}
			switch {
			case configExists:
				mod := modSrc.AsModule()
				err := mod.Serve(ctx, dagger.ModuleServeOpts{IncludeDependencies: true})
				if err != nil {
					return fmt.Errorf("failed to serve module: %w", err)
				}
				return fn(ctx, engineClient, mod, cmd, cmdArgs)
			case explicitModRefSet:
				// the user explicitly asked for a module but we didn't find one
				return fmt.Errorf("failed to get configured module: %w", err)
			default:
				// user didn't ask for a module, so just run in default mode since we didn't find one
				return fn(ctx, engineClient, nil, cmd, cmdArgs)
			}
		})
	}
}
