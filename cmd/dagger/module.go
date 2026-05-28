package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

	moduleURL         string
	moduleNoURL       bool
	allowedLLMModules []string

	installName   string
	workspaceHere bool

	eagerRuntime bool
)

const (
	moduleURLDefault = "."
)

func addWorkspaceInstallFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the module in the workspace. Defaults to the name of the module being installed.")
	addWorkspaceHereFlag(cmd)
}

// moduleAddFlags adds common module-loading flags to a command.
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

	addWorkspaceInstallFlags(moduleDepInstallCmd)
	addWorkspaceHereFlag(moduleDepUninstallCmd)

	setWorkspaceFlagPolicy(moduleUpdateCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(moduleDepInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(moduleDepUninstallCmd, workspaceFlagPolicyLocalOnly)
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

If no workspace config is selected, this creates one at the workspace root first.
Use --here to create the workspace config at the workspace cwd instead.`,
	Example: "dagger install github.com/shykes/daggerverse/hello@v0.3.0",
	GroupID: workspaceGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE:    runWorkspaceInstall,
}

var moduleDepUninstallCmd = &cobra.Command{
	Use:     "uninstall [options] <module>",
	Short:   "Uninstall a module",
	Long:    `Uninstall a module from the current workspace, removing it from .dagger/config.toml.`,
	Example: "dagger uninstall hello",
	GroupID: workspaceGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE:    runWorkspaceUninstall,
}

func runWorkspaceInstall(cmd *cobra.Command, extraArgs []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return installWorkspaceModule(ctx, cmd.OutOrStdout(), engineClient.Dagger(), extraArgs[0], installName, workspaceHere)
	})
}

func runWorkspaceUninstall(cmd *cobra.Command, extraArgs []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return uninstallWorkspaceModule(ctx, cmd.OutOrStdout(), engineClient.Dagger(), extraArgs[0], workspaceHere)
	})
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

// Wraps a command with optional module loading. If a module was explicitly specified by the user,
// it will try to load it and error out if it's not found or invalid. If no module was specified,
// it will try the current directory as a module but provide a nil module if it's not found, not
// erroring out.
func optionalModCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Module, *cobra.Command, []string) error,
	presetSecretToken string,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		_, explicitModRefSet := getExplicitModuleSourceRef()

		return withEngine(cmd.Context(), client.Params{
			SecretToken:          presetSecretToken,
			LoadWorkspaceModules: !moduleNoURL && !explicitModRefSet,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
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
