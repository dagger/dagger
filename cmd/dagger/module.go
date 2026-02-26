package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/go-git/go-git/v5"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/modules"
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

	installName      string
	installBlueprint bool

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

	// dagger module init
	moduleCmd.AddCommand(moduleModInitCmd)
	moduleModInitCmd.Flags().StringVar(&sdk, "sdk", "", "SDK to use (go, python, typescript)")
	moduleModInitCmd.Flags().StringVar(&moduleName, "name", "", "Module name (defaults to working directory name)")
	moduleModInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleModInitCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleModInitCmd.Flags().StringSliceVar(&moduleIncludes, "include", nil, "Paths to include when loading the module")
	moduleModInitCmd.Flags().BoolVar(&selfCalls, "with-self-calls", false, "Enable self-calls capability for the module (experimental)")

	modulePublishCmd.Flags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean")
	modulePublishCmd.Flags().StringVarP(&moduleURL, "mod", "m", "", "Module reference to publish, remote git repo (defaults to current directory)")

	// dagger module install
	moduleCmd.AddCommand(moduleDepInstallCmd)
	moduleDepInstallCmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	moduleDepInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleDepInstallCmd, moduleDepInstallCmd.Flags(), false)

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
}

// moduleModInitCmd is the "dagger module init" subcommand.
//
// Creates a new module. If a workspace config (.dagger/config.toml) exists,
// the module is created inside .dagger/modules/<name>/ and auto-installed.
// Otherwise, the module is created at the current directory or specified path
// without creating a workspace.
var moduleModInitCmd = &cobra.Command{
	Use:   "init [options] --sdk=<sdk> [name] [path]",
	Short: "Create a new module",
	Long: `Create a new module.

The module name can be given as the first positional argument or via --name.
If neither is given, it defaults to the current working directory name.

If a workspace config (.dagger/config.toml) already exists, the module is
created at .dagger/modules/<name>/ and automatically installed. Otherwise,
the module is created at the current directory (or the specified path) as
a standalone module.

A path can be given as the second positional argument to create the module
at that specific location without adding it to any workspace.`,
	Example: `  dagger module init --sdk=go
  dagger module init --sdk=go ci
  dagger module init --sdk=go --name=ci
  dagger module init --sdk=python deploy
  dagger module init --sdk=go mymod ./sub/mymod
  dagger module init --sdk=go --name=mymod --source=./sub/mymod`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		if remoteWorkdir != "" {
			return fmt.Errorf("cannot init module with a remote workdir")
		}

		if sdk == "" {
			return fmt.Errorf("--sdk is required; specify the SDK to use (go, python, typescript)")
		}

		// Resolve module name: positional arg > --name flag > workdir basename
		if len(extraArgs) >= 1 {
			if moduleName != "" {
				return fmt.Errorf("cannot specify module name as both a positional argument and --name flag")
			}
			moduleName = extraArgs[0]
		}
		if moduleName == "" {
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}
			moduleName = filepath.Base(wd)
		}

		// A second positional argument specifies a standalone module path.
		if len(extraArgs) >= 2 {
			standalonePath := extraArgs[1]
			if moduleSourcePath != "" {
				return fmt.Errorf("cannot specify both a positional path and --source flag")
			}
			return initStandaloneModule(ctx, cmd, moduleName, standalonePath, "")
		}

		return initWorkspaceModule(ctx, cmd, moduleName)
	},
}

// initWorkspaceModule creates a module inside .dagger/modules/<name>/ and
// auto-installs it in .dagger/config.toml.
func initWorkspaceModule(ctx context.Context, cmd *cobra.Command, modName string) error {
	return withEngine(ctx, client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) (err error) {
		dag := engineClient.Dagger()

		ws := dag.CurrentWorkspace()

		msg, err := ws.ModuleInit(ctx, modName, sdk, dagger.WorkspaceModuleInitOpts{
			Source:  moduleSourcePath,
			Include: moduleIncludes,
			License: licenseID,
		})
		if err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), msg)
		return nil
	})
}

// initStandaloneModule creates a module at the given path with its own
// dagger.json, without adding it to any workspace config. If sourceSubpath
// is non-empty, the module source is placed in that subdirectory relative
// to modPath (e.g. modPath="." sourceSubpath="dagger" creates dagger.json
// at "." with source in "./dagger/").
func initStandaloneModule(ctx context.Context, cmd *cobra.Command, modName string, modPath string, sourceSubpath string) error {
	return withEngine(ctx, client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) (err error) {
		dag := engineClient.Dagger()

		modSrc := dag.ModuleSource(modPath, dagger.ModuleSourceOpts{
			DisableFindUp:  true,
			AllowNotExists: true,
			RequireKind:    dagger.ModuleSourceKindLocalSource,
		})

		alreadyExists, err := modSrc.ConfigExists(ctx)
		if err != nil {
			return fmt.Errorf("failed to check if module already exists: %w", err)
		}
		if alreadyExists {
			return fmt.Errorf("module already exists at %s", modPath)
		}

		contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
		if err != nil {
			return fmt.Errorf("failed to get local context directory path: %w", err)
		}

		modSrc = modSrc.WithName(modName)
		modSrc = modSrc.WithSDK(sdk)
		if sourceSubpath != "" {
			modSrc = modSrc.WithSourceSubpath(sourceSubpath)
		}
		if len(moduleIncludes) > 0 {
			modSrc = modSrc.WithIncludes(moduleIncludes)
		}
		if selfCalls {
			modSrc = modSrc.WithExperimentalFeatures([]dagger.ModuleSourceExperimentalFeature{dagger.ModuleSourceExperimentalFeatureSelfCalls})
		}
		modSrc = modSrc.WithEngineVersion(modules.EngineVersionLatest)

		_, err = modSrc.GeneratedContextDirectory().Export(ctx, contextDirPath)
		if err != nil {
			return fmt.Errorf("failed to generate module: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Initialized module", modName, "in", contextDirPath)
		return nil
	})
}

var moduleUpdateCmd = &cobra.Command{
	Use:     "update [options] [<DEPENDENCY>...]",
	Aliases: []string{"use"},
	Short:   "Update a module's dependencies",
	Long: `Update the dependencies of a local module.

To update only specific dependencies, specify their short names or a complete address.

If no dependency is specified, all dependencies are updated.
`,
	Example: `"dagger update" or "dagger update hello" "dagger update github.com/shykes/daggerverse/hello@v0.3.0"`,
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
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

var moduleDepInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Aliases: []string{"use"},
	Short:   "Add a module dependency",
	Long:    "Add another module as a dependency of the current module, updating its dagger.json.",
	Example: `  dagger module install github.com/shykes/daggerverse/hello@v0.3.0
  dagger module install ./path/to/local/module`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
			EagerRuntime:         eagerRuntime,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
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

			depRefStr := extraArgs[0]
			depSrc := dag.ModuleSource(depRefStr, dagger.ModuleSourceOpts{
				DisableFindUp: true,
			})

			if installName != "" {
				depSrc = depSrc.WithName(installName)
			}

			modSrc = modSrc.WithDependencies([]*dagger.ModuleSource{depSrc})
			if engineVersion := getCompatVersion(); engineVersion != "" {
				modSrc = modSrc.WithEngineVersion(engineVersion)
			}

			_, err = modSrc.
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to install dependency: %w", err)
			}

			depName, err := depSrc.ModuleName(ctx)
			if err != nil {
				return err
			}
			if installName != "" {
				depName = installName
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Installed module dependency %q\n", depName)
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
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
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
		return withEngine(ctx, client.Params{
			// develop only generates code — it doesn't need workspace
			// modules loaded (which would fail for codegen-only SDKs).
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
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
				return fmt.Errorf("failed to check if module exists: %w", err)
			}
			switch {
			case configExists:
				serveCtx, span := Tracer().Start(ctx, "load module: "+modRef)
				mod := modSrc.AsModule()
				serveErr := mod.Serve(serveCtx, dagger.ModuleServeOpts{IncludeDependencies: true})
				telemetry.EndWithCause(span, &serveErr)
				if serveErr != nil {
					return fmt.Errorf("failed to serve module: %w", serveErr)
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
