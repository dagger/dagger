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
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	workspaceGroup = &cobra.Group{
		ID:    "workspace",
		Title: "Dagger Workspace Commands",
	}

	moduleGroup = &cobra.Group{
		ID:    "module",
		Title: "Dagger Module Commands",
	}

	moduleCmd = &cobra.Command{
		Use:     "module",
		Short:   "Manage modules",
		GroupID: moduleGroup.ID,
	}

	moduleURL         string
	moduleNoURL       bool
	allowedLLMModules []string

	sdk               string
	deprecatedLicense string
	compatVersion     string

	moduleName       string
	moduleSourcePath string
	moduleIncludes   []string

	installName   string
	initBlueprint string

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

func initRequestedChanges(cmd *cobra.Command) bool {
	for _, name := range []string{
		"sdk",
		"name",
		"source",
		"include",
		"blueprint",
		"with-self-calls",
	} {
		if flag := cmd.Flags().Lookup(name); flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

func addWorkspaceInstallFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the module in the workspace. Defaults to the name of the module being installed.")
}

func addDeprecatedLicenseFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&deprecatedLicense, "license", "false", "Deprecated: automatic license generation has been removed")
	flag := cmd.Flags().Lookup("license")
	flag.Hidden = true
	flag.NoOptDefVal = "true"
}

func checkDeprecatedLicenseFlag(cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup("license")
	if flag == nil || !flag.Changed {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(flag.Value.String())) {
	case "", "false":
		return nil
	default:
		return fmt.Errorf("--license is deprecated and no longer generates a LICENSE file; create one manually")
	}
}

func addModuleDependencyInstallFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	cmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(cmd, cmd.Flags(), false)
}

func addModuleDependencyUpdateFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(cmd, cmd.Flags(), false)
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

	moduleInitCmd.Flags().StringVar(&sdk, "sdk", "", "Optionally install a Dagger SDK")
	moduleInitCmd.Flags().StringVar(&moduleName, "name", "", "Name of the new module (defaults to parent directory name)")
	moduleInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	addDeprecatedLicenseFlag(moduleInitCmd)
	moduleInitCmd.Flags().StringSliceVar(&moduleIncludes, "include", nil, "Paths to include when loading the module. Only needed when extra paths are required to build the module. They are expected to be relative to the directory containing the module's dagger.json file (the module source root).")
	moduleInitCmd.Flags().StringVar(&initBlueprint, "blueprint", "", "Reference another module as blueprint")
	moduleInitCmd.Flags().BoolVar(&selfCalls, "with-self-calls", false, "Enable self-calls capability for the module (experimental)")

	modulePublishCmd.Flags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean")
	modulePublishCmd.Flags().StringVarP(&moduleURL, "mod", "m", "", "Module reference to publish, remote git repo (defaults to current directory)")

	addWorkspaceInstallFlags(moduleDepInstallCmd)
	addModuleDependencyInstallFlags(moduleModInstallCmd)

	moduleUnInstallCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleAddFlags(moduleUnInstallCmd, moduleUnInstallCmd.Flags(), false)

	addModuleDependencyUpdateFlags(moduleModUpdateCmd)

	moduleCmd.AddCommand(moduleInitCmd, moduleModInstallCmd, moduleModUpdateCmd)

	moduleDevelopCmd.Flags().StringVar(&developSDK, "sdk", "", "Install the given Dagger SDK. Can be builtin (go, python, typescript) or a module address")
	moduleDevelopCmd.Flags().StringVar(&developSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleDevelopCmd.Flags().BoolVarP(&developRecursive, "recursive", "r", false, "Develop recursively into local dependencies")
	addDeprecatedLicenseFlag(moduleDevelopCmd)
	moduleDevelopCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleDevelopCmd.Flags().Lookup("compat").NoOptDefVal = "skip"
	moduleDevelopCmd.Flags().BoolVar(&selfCalls, "with-self-calls", false, "Enable self-calls capability for the module (experimental)")
	moduleDevelopCmd.Flags().BoolVar(&noSelfCalls, "without-self-calls", false, "Disable self-calls capability for the module")
	moduleAddFlags(moduleDevelopCmd, moduleDevelopCmd.Flags(), false)

	setWorkspaceFlagPolicy(moduleCmd, workspaceFlagPolicyDisallow)
	setWorkspaceFlagPolicy(moduleInitCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(moduleUpdateCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(moduleDepInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(moduleUnInstallCmd, workspaceFlagPolicyDisallow)
	setWorkspaceFlagPolicy(moduleDevelopCmd, workspaceFlagPolicyDisallow)
	setWorkspaceFlagPolicy(modulePublishCmd, workspaceFlagPolicyDisallow)
}

var moduleInitCmd = &cobra.Command{
	Use:   "init [options] [path]",
	Short: "Initialize a new module",
	Long: `Initialize a new module at the given path.

This creates a dagger.json file at the specified directory, making it the root of the new module.

If no path is provided and the current workspace is already initialized, the module is created
inside .dagger/modules/<name>/ and automatically installed in .dagger/config.toml.
Use an explicit path to keep creating a standalone module instead.

If --sdk is specified, the given SDK is installed in the module. You can do this later with "dagger develop".
If --blueprint is specified, the given blueprint is installed in the module.
`,
	Example: `
# Reference a remote module as blueprint
dagger module init --blueprint=github.com/example/blueprint

# Reference a local module as blueprint
dagger module init --blueprint=../my/blueprints/simple-webapp

# Implement a standalone module in Go
dagger module init --sdk=go
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		if err := checkDeprecatedLicenseFlag(cmd); err != nil {
			return err
		}

		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
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
				if !initRequestedChanges(cmd) {
					return nil
				}
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

			// Standalone init supports targeting a directory that does not exist yet.
			// Create it before SDK setup probes the host path so progress output
			// does not show false "not found" errors for a path we are about to export.
			if _, err := os.Stat(srcRootAbsPath); os.IsNotExist(err) {
				if err := os.MkdirAll(srcRootAbsPath, 0o755); err != nil {
					return fmt.Errorf("failed to create module directory: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("failed to stat module directory: %w", err)
			}

			if len(extraArgs) == 0 {
				hasConfig, err := dag.CurrentWorkspace().HasConfig(ctx)
				if err != nil {
					return fmt.Errorf("failed to load workspace config state: %w", err)
				}
				if hasConfig {
					if moduleSourcePath != "" && filepath.IsAbs(moduleSourcePath) {
						return fmt.Errorf("--source must be relative when initializing a workspace module")
					}
					return initWorkspaceModule(ctx, cmd.OutOrStdout(), dag, workspaceModuleInitOptions{
						Name:      moduleName,
						SDK:       sdk,
						Source:    moduleSourcePath,
						Include:   moduleIncludes,
						Blueprint: initBlueprint,
						SelfCalls: selfCalls,
					})
				}
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
				// Install the blueprint while the CLI still supports the legacy flag.
				modSrc = modSrc.WithBlueprint(blueprintSrc) //nolint:staticcheck
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

var moduleUpdateCmd = &cobra.Command{
	Use:   "update [module...]",
	Short: "Refresh workspace-managed state",
	Long: `Refresh workspace-managed state.

With no module names, refresh entries already recorded in .dagger/lock.

With module names, refresh only those modules from .dagger/config.toml.
`,
	Example: `"dagger update" or "dagger update wolfi"`,
	GroupID: workspaceGroup.ID,
	RunE:    runWorkspaceUpdate,
}

var moduleDepInstallCmd = &cobra.Command{
	Use:   "install [options] <module>",
	Short: "Install a module",
	Long: `Install a module into the current workspace.

If the current directory is not yet a workspace, this initializes one first.`,
	Example: "dagger install github.com/shykes/daggerverse/hello@v0.3.0",
	GroupID: workspaceGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			return installWorkspaceModule(ctx, cmd.OutOrStdout(), engineClient.Dagger(), extraArgs[0], installName)
		})
	},
}

var moduleModInstallCmd = &cobra.Command{
	Use:   "install [options] <module>",
	Short: "Install a module dependency",
	Long: `Install a module dependency into the current module.

This always updates the current module's dagger.json, even when run inside an
initialized workspace.`,
	Example: `dagger module install github.com/shykes/daggerverse/hello@v0.3.0
  dagger module install ./path/to/local/module`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
			EagerRuntime:         eagerRuntime,
		}, func(ctx context.Context, engineClient *client.Client) error {
			depName, err := installModuleDependency(ctx, engineClient.Dagger(), extraArgs[0])
			if err != nil {
				return err
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
		return modifyLocalModule(ctx, func(dag *dagger.Client, modSrc *dagger.ModuleSource) *dagger.ModuleSource {
			return modSrc.WithoutDependencies(extraArgs)
		})
	},
}

var moduleModUpdateCmd = &cobra.Command{
	Use:   "update [options] [<DEPENDENCY>...]",
	Short: "Update a module's dependencies",
	Long: `Update the dependencies of the current module.

To update only specific dependencies, specify their short names or a complete address.

If no dependency is specified, all dependencies are updated.
`,
	Example: `"dagger module update" or "dagger module update hello" "dagger module update github.com/shykes/daggerverse/hello@v0.3.0"`,
	RunE:    runModuleDependencyUpdate,
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
		if err := checkDeprecatedLicenseFlag(cmd); err != nil {
			return err
		}
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
					modSourcePath, err := modSrc.SourceSubpath(ctx)
					if err != nil {
						return fmt.Errorf("failed to get module source subpath: %w", err)
					}

					requestedSourcePath := developSourcePath
					if developSDK != "" {
						if modSDK != "" && modSDK != developSDK {
							return fmt.Errorf("cannot update module SDK that has already been set to %q", modSDK)
						}
						modSDK = developSDK
						modSrc = modSrc.WithSDK(modSDK)
					}

					// if SDK is set but source path isn't and the user didn't provide --source, we'll use the default source path
					if modSDK != "" && modSourcePath == "" && requestedSourcePath == "" {
						inferredSourcePath, err := inferSourcePathDir(srcRootPath)
						if err != nil {
							return err
						}

						requestedSourcePath = filepath.Join(srcRootPath, inferredSourcePath)
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

					if requestedSourcePath != "" {
						// ensure source path is relative to the source root
						sourceAbsPath, err := pathutil.Abs(requestedSourcePath)
						if err != nil {
							return fmt.Errorf("failed to get absolute source path for %s: %w", requestedSourcePath, err)
						}
						requestedSourcePath, err = filepath.Rel(srcRootPath, sourceAbsPath)
						if err != nil {
							return fmt.Errorf("failed to get relative source path: %w", err)
						}

						// Compare against the configured source path captured before WithSDK,
						// since WithSDK defaults an unset source path to the module root.
						if modSourcePath != "" && modSourcePath != requestedSourcePath {
							return fmt.Errorf("cannot update module source path that has already been set to %q", modSourcePath)
						}

						modSourcePath = requestedSourcePath
						modSrc = modSrc.WithSourceSubpath(modSourcePath)
					}

					contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
					if err != nil {
						return localModuleErrorf("failed to get local context directory path: %w", err)
					}

					cs, err := modSrc.GeneratedContextChangeset().Sync(ctx)
					if err != nil {
						return fmt.Errorf("failed to generate code: %w", err)
					}

					if _, err := cs.Export(ctx, contextDirPath); err != nil {
						return fmt.Errorf("failed to apply generated code: %w", err)
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

// modifyLocalModule is a helper that loads a local module source, applies a
// transformation, and exports the generated context directory back to disk.
func modifyLocalModule(ctx context.Context, transform func(*dagger.Client, *dagger.ModuleSource) *dagger.ModuleSource) error {
	return withEngine(ctx, client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		modSrc, contextDirPath, err := loadLocalModuleForMutation(ctx, dag)
		if err != nil {
			return err
		}

		modSrc = transform(dag, modSrc)
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
}

func installModuleDependency(ctx context.Context, dag *dagger.Client, depRef string) (string, error) {
	modSrc, contextDirPath, err := loadLocalModuleForMutation(ctx, dag)
	if err != nil {
		return "", err
	}

	return installModuleDependencyTo(ctx, dag, modSrc, contextDirPath, depRef)
}

func installModuleDependencyTo(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource, contextDirPath, depRef string) (string, error) {
	depSrc := dag.ModuleSource(depRef, dagger.ModuleSourceOpts{
		DisableFindUp: true,
	})
	if installName != "" {
		depSrc = depSrc.WithName(installName)
	}

	modSrc = modSrc.WithDependencies([]*dagger.ModuleSource{depSrc})
	if engineVersion := getCompatVersion(); engineVersion != "" {
		modSrc = modSrc.WithEngineVersion(engineVersion)
	}

	_, err := modSrc.GeneratedContextDirectory().Export(ctx, contextDirPath)
	if err != nil {
		return "", fmt.Errorf("failed to install dependency: %w", err)
	}

	if installName != "" {
		return installName, nil
	}
	return depSrc.ModuleName(ctx)
}

func updateModuleDependencies(ctx context.Context, dag *dagger.Client, deps []string) error {
	modSrc, contextDirPath, err := loadLocalModuleForMutation(ctx, dag)
	if err != nil {
		return err
	}

	modSrc = modSrc.WithUpdateDependencies(deps)
	if engineVersion := getCompatVersion(); engineVersion != "" {
		modSrc = modSrc.WithEngineVersion(engineVersion)
	}

	_, err = modSrc.GeneratedContextDirectory().Export(ctx, contextDirPath)
	if err != nil {
		return fmt.Errorf("failed to update dependencies: %w", err)
	}

	return nil
}

func runModuleDependencyUpdate(cmd *cobra.Command, deps []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateModuleDependencies(ctx, engineClient.Dagger(), deps)
	})
}

func loadLocalModuleForMutation(ctx context.Context, dag *dagger.Client) (*dagger.ModuleSource, string, error) {
	modRef, err := getModuleSourceRefWithDefault()
	if err != nil {
		return nil, "", err
	}
	modSrc := dag.ModuleSource(modRef, dagger.ModuleSourceOpts{
		RequireKind: dagger.ModuleSourceKindLocalSource,
	})

	alreadyExists, err := modSrc.ConfigExists(ctx)
	if err != nil {
		return nil, "", localModuleErrorf("failed to check if module already exists: %w", err)
	}
	if !alreadyExists {
		return nil, "", fmt.Errorf("module must be fully initialized")
	}

	contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
	if err != nil {
		return nil, "", localModuleErrorf("failed to get local context directory path: %w", err)
	}

	return modSrc, contextDirPath, nil
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
			SecretToken:          presetSecretToken,
			SkipWorkspaceModules: moduleNoURL,
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
