package daggercmd

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
	moduleURL              string
	moduleNoURL            bool
	allowedLLMModules      []string
	allowedHostPortModules []string

	installName   string
	workspaceHere bool

	eagerRuntime bool
)

const (
	moduleURLDefault = "."
	coreModuleRef    = "core"
)

func addWorkspaceInstallFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the module in the workspace. Defaults to the name of the module being installed.")
	addWorkspaceHereFlag(cmd)
}

// moduleAddFlags adds common module-loading flags to a command.
// If optional is true, it also adds the --no-load-module flag and marks --load-module and --no-load-module as mutually exclusive.
func moduleAddFlags(cmd *cobra.Command, flags *pflag.FlagSet, optional bool) {
	flags.StringVarP(&moduleURL, "load-module", "m", "", "Use a one-off module (local path or git ref)")
	if optional {
		flags.BoolVarP(&moduleNoURL, "no-load-module", "M", false, "Don't load any module for this command")
		cmd.MarkFlagsMutuallyExclusive("load-module", "no-load-module")
	}

	var defaultAllowLLM []string
	if allowLLMEnv := os.Getenv("DAGGER_ALLOW_LLM"); allowLLMEnv != "" {
		defaultAllowLLM = strings.Split(allowLLMEnv, ",")
	}
	flags.StringSliceVar(&allowedLLMModules, "allow-llm", defaultAllowLLM, "List of URLs of remote modules allowed to access LLM APIs, or 'all' to bypass restrictions for the entire session")

	flags.StringSliceVar(&allowedHostPortModules, "allow-host-ports", defaultAllowedHostPortModules(), "List of local/Git modules allowed to publish ports on the host, or 'local'/'all' to allow broader scopes")

	// Add the eager module loading flag to disable lazy load on runtime.
	flags.BoolVar(&eagerRuntime, "eager-runtime", false, "load module runtime eagerly")
}

func defaultAllowedHostPortModules() []string {
	if allowHostPortsEnv := os.Getenv("DAGGER_ALLOW_HOST_PORTS"); allowHostPortsEnv != "" {
		return strings.Split(allowHostPortsEnv, ",")
	}
	return nil
}

func init() {
	moduleAddFlags(apiCallCmd.Command(), apiCallCmd.Command().PersistentFlags(), true)
	moduleAddFlags(callModCmd.Command(), callModCmd.Command().PersistentFlags(), true)

	moduleAddFlags(apiFunctionsCmd, apiFunctionsCmd.PersistentFlags(), false)
	moduleAddFlags(functionsAliasCmd, functionsAliasCmd.PersistentFlags(), false)
	moduleAddFlags(apiListenCmd, apiListenCmd.PersistentFlags(), true)
	moduleAddFlags(listenCmd, listenCmd.PersistentFlags(), true)
	moduleAddFlags(apiQueryCmd, apiQueryCmd.PersistentFlags(), true)
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

var moduleUpdateCmd = newWorkspaceUpdateCmd(false)

func newWorkspaceUpdateCmd(hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Refresh installed-module state",
		Long: `Refresh installed-module state.

Refreshes entries already recorded in dagger.lock.`,
		Example: `"dagger update"`,
		Args:    cobra.NoArgs,
		Hidden:  hidden,
		RunE:    runWorkspaceUpdate,
	}
}

var moduleDepInstallCmd = newWorkspaceInstallCmd(false, []string{"i"})

func newWorkspaceInstallCmd(hidden bool, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     "install [options] <module>",
		Aliases: aliases,
		Short:   "Install a module into your workspace",
		Long: `Install a module into the current workspace.

If no workspace config is selected, this creates one at the workspace root first.
Use --here to create the workspace config at the workspace cwd instead.`,
		Example: "dagger install github.com/shykes/daggerverse/hello@v0.3.0",
		Hidden:  hidden,
		Args:    cobra.ExactArgs(1),
		RunE:    runWorkspaceInstall,
	}
}

var moduleDepUninstallCmd = newWorkspaceUninstallCmd(false, []string{"un"})

func newWorkspaceUninstallCmd(hidden bool, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall [options] <module>",
		Aliases: aliases,
		Short:   "Uninstall a module from your workspace",
		Long:    `Uninstall a module from the current workspace, removing it from dagger.toml.`,
		Example: "dagger uninstall hello",
		Hidden:  hidden,
		Args:    cobra.ExactArgs(1),
		RunE:    runWorkspaceUninstall,
	}
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

func isCoreModuleRef(ref string) bool {
	return ref == coreModuleRef
}

func isCoreModuleSelected() bool {
	ref, ok := getExplicitModuleSourceRef()
	return ok && isCoreModuleRef(ref)
}

func getModuleSourceRefWithDefault() (string, error) {
	if v, ok := getExplicitModuleSourceRef(); ok {
		return v, nil
	}
	if moduleNoURL {
		return "", fmt.Errorf("cannot use default module source with --no-load-module")
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
			LoadWorkspaceModules: !moduleNoURL && !explicitModRefSet && !isCoreModuleSelected(),
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			if moduleNoURL || isCoreModuleSelected() {
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
