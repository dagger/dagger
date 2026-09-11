package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const sdkModuleSettingAnnotation = "sdk-module-setting"

var (
	moduleInitName        string
	moduleInitPath        string
	moduleInitInstall     bool
	moduleInitEntrypoint  bool
	moduleInitNoApply     bool
	moduleClientAddSDK    string
	moduleClientRemoveSDK string
	moduleClientScopeSDK  string
	moduleClientListAll   bool
	moduleClientListSDK   string
	moduleClientUpdateAll bool
	moduleClientUpdateSDK string
)

var moduleInitCmd = &cobra.Command{
	Use:   "init SDK [flags]",
	Short: "Initialize a new module for development with an SDK",
	Long:  "Initialize a new module for development with an SDK",
	Args:  cobra.NoArgs,
	Annotations: map[string]string{
		availableSubcommandsTitleAnnotation: "AVAILABLE SDKs",
		noAvailableSubcommandsAnnotation:    "NO AVAILABLE SDKs. In doubt, try 'dagger mod install github.com/dagger/dang-sdk'",
	},
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var moduleClientCmd = &cobra.Command{
	Use:   "client",
	Short: "Manage generated clients for modules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var moduleClientAddCmd = &cobra.Command{
	Use:   "add <module> [--sdk=SDK]",
	Short: "Add and generate a module client",
	Long: `Add a module client to one SDK scope and generate that scope.

Use an explicit local path such as ./api or ../api, or a module address.
Installed module names are not supported.

With no --sdk, select the deepest scope found across installed SDKs.
If several SDKs have that scope, use --sdk to select one.`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSDKModuleClientAdd(cmd, args[0])
	},
}

var moduleClientRemoveCmd = &cobra.Command{
	Use:   "rm <module> [--sdk=SDK]",
	Short: "Remove a module client",
	Long: `Remove a recorded module client and regenerate its SDK scope.

Use the exact TARGET from 'dagger module client list'. Select the deepest
matching scope. If several SDKs have that scope, use --sdk to select one.
If invalid targets remain, save the removal and skip generation until they
are corrected or removed.`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSDKModuleClientRemove(cmd, args[0])
	},
}

var moduleClientUpdateCmd = &cobra.Command{
	Use:   "update [module...]",
	Short: "Update module clients",
	Long: `Update the recorded module clients and regenerate their SDK scopes.

With no argument, updates every client target in the current scope. Only the
lock entries that the selected targets reach are rewritten.`,
	Example: "dagger module client update",
	RunE:    runSDKModuleClientUpdate,
}

var moduleClientScopeCmd = &cobra.Command{
	Use:   "scope",
	Short: "Print the current client-generation scope",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runSDKModuleClientScope(cmd)
	},
}

var moduleClientListCmd = &cobra.Command{
	Use:   "list",
	Short: "List generated module clients",
	Long: `List recorded clients in scopes that contain the current directory.

Use --all to list clients in every scope. SCOPE is relative to the workspace
root. To remove a row, run 'dagger module client rm TARGET --sdk=SDK' from SCOPE.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runSDKModuleClientList(cmd)
	},
}

func init() {
	moduleInitCmd.PersistentFlags().StringVarP(&moduleInitName, "name", "n", "", "Module name (inferred when omitted)")
	moduleInitCmd.PersistentFlags().StringVar(&moduleInitPath, "path", "", "Module path (default: .dagger/modules/<name> beside dagger.toml)")
	moduleInitCmd.PersistentFlags().BoolVar(&moduleInitInstall, "install", false, "Install the module (default: install when --path is omitted)")
	moduleInitCmd.PersistentFlags().BoolVar(&moduleInitEntrypoint, "entrypoint", false, "Install and select the module as entrypoint (default: select when --path and --name are omitted)")
	moduleInitCmd.PersistentFlags().BoolVar(&moduleInitNoApply, "no-apply", false, "Show generated changes without applying them")
	moduleClientAddCmd.Flags().StringVar(&moduleClientAddSDK, "sdk", "", "Select an installed `SDK` (default: deepest scope)")
	moduleClientRemoveCmd.Flags().StringVar(&moduleClientRemoveSDK, "sdk", "", "Select an installed `SDK` (default: deepest matching scope)")
	moduleClientScopeCmd.Flags().StringVar(&moduleClientScopeSDK, "sdk", "", "SDK module to query")
	_ = moduleClientScopeCmd.MarkFlagRequired("sdk")
	moduleClientListCmd.Flags().BoolVar(&moduleClientListAll, "all", false, "List clients in all scopes")
	moduleClientListCmd.Flags().StringVar(&moduleClientListSDK, "sdk", "", "Filter by SDK module")
	moduleClientUpdateCmd.Flags().BoolVar(&moduleClientUpdateAll, "all", false, "Update clients in all scopes")
	moduleClientUpdateCmd.Flags().StringVar(&moduleClientUpdateSDK, "sdk", "", "Filter by SDK module")

	moduleClientCmd.AddCommand(
		moduleClientAddCmd,
		moduleClientRemoveCmd,
		moduleClientListCmd,
		moduleClientScopeCmd,
		moduleClientUpdateCmd,
	)
}

func runSDKModuleInit(cmd *cobra.Command, sdk string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("module init does not support --env; SDK scopes live in the base workspace config")
	}
	install, entrypoint := moduleInitControl(moduleInitCmd.PersistentFlags(), "install"), moduleInitControl(moduleInitCmd.PersistentFlags(), "entrypoint")
	plan, err := workspace.PlanModuleInit(moduleInitPath != "", moduleInitName != "", install, entrypoint)
	if err != nil {
		return err
	}
	settings, err := sdkModuleSettingsJSON(cmd, sdk)
	if err != nil {
		return err
	}
	disposition := changesetDispositionForAutoApply(autoApply)
	if moduleInitNoApply {
		disposition = changesetDispositionNoApply
	}
	return mutateSDKModuleWorkspaceWithDisposition(cmd, `
query ModuleInit($sdk: String!, $name: String, $path: String, $settings: JSON, $install: Boolean, $entrypoint: Boolean) {
  currentWorkspace {
    result: withInitModule(sdk: $sdk, name: $name, path: $path, settings: $settings, install: $install, entrypoint: $entrypoint) { id }
  }
}`, map[string]any{
		"sdk":        sdk,
		"name":       moduleInitName,
		"path":       moduleInitPath,
		"settings":   dagger.JSON(settings),
		"install":    install,
		"entrypoint": entrypoint,
	}, disposition, func(ctx context.Context, current *dagger.Workspace) error {
		name, err := moduleInitResultName(ctx, current)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), moduleInitSuccessMessage(name, moduleInitPath, plan))
		return err
	})
}

func moduleInitControl(flags *pflag.FlagSet, name string) *bool {
	if !flags.Changed(name) {
		return nil
	}
	value, _ := flags.GetBool(name)
	return &value
}

func moduleInitResultName(ctx context.Context, current *dagger.Workspace) (string, error) {
	if moduleInitName != "" {
		return moduleInitName, nil
	}
	configFile, err := current.ConfigFile(ctx)
	if err != nil {
		return "", err
	}
	cwd, err := current.Cwd(ctx)
	if err != nil {
		return "", err
	}
	address, err := current.Address(ctx)
	if err != nil {
		return "", err
	}
	return moduleInitNameFromWorkspace(configFile, cwd, address, moduleInitPath)
}

func moduleInitNameFromWorkspace(configFile, cwd, address, modulePath string) (string, error) {
	if configFile != "" {
		var err error
		configFile, err = workspaceConfigRootPathFromCwd(configFile, cwd)
		if err != nil {
			return "", err
		}
	}
	cwd, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	scopePath := ""
	if modulePath != "" {
		base := cwd
		modulePath = strings.ReplaceAll(modulePath, `\`, "/")
		if filepath.IsAbs(modulePath) {
			base = "."
			modulePath = strings.TrimLeft(modulePath, "/")
		}
		scopePath, err = workspace.ResolveSDKManagedPath(base, modulePath)
		if err != nil {
			return "", err
		}
	}
	return workspace.ModuleInitName("", scopePath, filepath.Dir(configFile), workspace.ModuleRootDirectoryName("", address, cwd))
}

func moduleInitSuccessMessage(name, modulePath string, plan workspace.ModuleInitPlan) string {
	if !plan.Install {
		if modulePath != "" {
			return moduleInitCustomPathMessage(modulePath)
		}
		return fmt.Sprintf("Initialized module %q.\nModule was not installed.\n", name)
	}
	if plan.Entrypoint && !plan.AutomaticEntrypoint {
		return fmt.Sprintf("Installed module %q as entrypoint.\n", name)
	}
	if plan.AutomaticInstall {
		if plan.Entrypoint {
			return fmt.Sprintf("Automatically installed module %q as entrypoint.\n", name)
		}
		return fmt.Sprintf("Automatically installed module %q.\n", name)
	}
	message := fmt.Sprintf("Installed module %q.\n", name)
	if plan.AutomaticEntrypoint {
		message += fmt.Sprintf("Automatically selected module %q as entrypoint.\n", name)
	}
	return message
}

func moduleInitCustomPathMessage(modulePath string) string {
	if modulePath == "" {
		return ""
	}
	return fmt.Sprintf(
		"Initialized module %s\nCustom path; module was not installed.\n",
		filepath.ToSlash(filepath.Clean(modulePath)),
	)
}

func runSDKModuleClientAdd(cmd *cobra.Command, module string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("module client add does not support --env; SDK scopes live in the base workspace config")
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleClientAdd($module: String!, $sdk: String) {
  currentWorkspace {
    result: withClient(module: $module, sdk: $sdk) { id }
  }
}`, map[string]any{
		"module": module,
		"sdk":    moduleClientAddSDK,
	}, nil)
}

func runSDKModuleClientRemove(cmd *cobra.Command, module string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("module client rm does not support --env; SDK scopes live in the base workspace config")
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleClientRemove($module: String!, $sdk: String) {
  currentWorkspace {
    result: withoutClient(module: $module, sdk: $sdk) { id }
  }
}`, map[string]any{"module": module, "sdk": moduleClientRemoveSDK}, nil)
}

func runSDKModuleClientUpdate(cmd *cobra.Command, modules []string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("module client update does not support --env; SDK scopes live in the base workspace config")
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleClientUpdate($modules: [String!], $all: Boolean, $sdk: String) {
  currentWorkspace {
    result: withUpdatedClients(modules: $modules, all: $all, sdk: $sdk) { id }
  }
}`, map[string]any{
		"modules": modules,
		"all":     moduleClientUpdateAll,
		"sdk":     moduleClientUpdateSDK,
	}, nil)
}

func mutateSDKModuleWorkspace(
	cmd *cobra.Command,
	query string,
	variables map[string]any,
	afterApply func(context.Context, *dagger.Workspace) error,
) error {
	return mutateSDKModuleWorkspaceWithDisposition(cmd, query, variables, changesetDispositionForAutoApply(autoApply), afterApply)
}

func mutateSDKModuleWorkspaceWithDisposition(
	cmd *cobra.Command,
	query string,
	variables map[string]any,
	disposition changesetDisposition,
	afterApply func(context.Context, *dagger.Workspace) error,
) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		var result struct {
			CurrentWorkspace struct {
				Result struct {
					ID dagger.ID
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{Query: query, Variables: variables}, &dagger.Response{Data: &result}); err != nil {
			return err
		}
		if result.CurrentWorkspace.Result.ID == "" {
			return fmt.Errorf("SDK-module workspace operation returned no workspace")
		}

		current := dag.CurrentWorkspace()
		updated := dagger.Ref[*dagger.Workspace](dag, result.CurrentWorkspace.Result.ID)
		applied, err := handleWorkspaceResponseWithDisposition(ctx, dag, current, updated, disposition, cmd.OutOrStdout())
		if err != nil || !applied || afterApply == nil {
			return err
		}
		return afterApply(ctx, current)
	})
}

func runSDKModuleClientScope(cmd *cobra.Command) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		var result struct {
			CurrentWorkspace struct {
				DetectScope string
			}
		}
		if err := ec.Dagger().Do(ctx, &dagger.Request{
			Query:     `query ModuleClientScope($sdk: String!) { currentWorkspace { detectScope(sdk: $sdk) } }`,
			Variables: map[string]any{"sdk": moduleClientScopeSDK},
		}, &dagger.Response{Data: &result}); err != nil {
			return err
		}
		if result.CurrentWorkspace.DetectScope == "" {
			return nil
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), result.CurrentWorkspace.DetectScope)
		return err
	})
}

func runSDKModuleClientList(cmd *cobra.Command) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		state, err := loadSDKWorkspaceConfig(ctx, ec.Dagger().CurrentWorkspace(), false)
		if err != nil || state == nil {
			return err
		}
		cfg := state.config
		cwd := state.cwd
		configDir := state.configDir
		if cfg == nil {
			return nil
		}

		type row struct{ scope, sdk, target string }
		var rows []row
		for sdkName, entry := range cfg.SDKs {
			if moduleClientListSDK != "" && sdkName != moduleClientListSDK {
				continue
			}
			for configScope, scope := range entry.Scopes {
				workspaceScope, err := workspace.ResolveSDKManagedPath(configDir, configScope)
				if err != nil {
					return err
				}
				if !moduleClientListAll && !cliWorkspacePathContains(workspaceScope, cwd) {
					continue
				}
				for _, target := range scope.Clients {
					rows = append(rows, row{workspaceScope, sdkName, target})
				}
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].scope != rows[j].scope {
				return rows[i].scope < rows[j].scope
			}
			if rows[i].sdk != rows[j].sdk {
				return rows[i].sdk < rows[j].sdk
			}
			return rows[i].target < rows[j].target
		})
		if len(rows) == 0 {
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "SCOPE\tSDK\tTARGET"); err != nil {
			return err
		}
		for _, row := range rows {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", row.scope, row.sdk, row.target); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

func sdkModuleSettingsJSON(cmd *cobra.Command, selectedSDK string) (string, error) {
	settings := map[string]any{}
	var visitErr error
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if visitErr != nil {
			return
		}
		annotation := flag.Annotations[sdkModuleSettingAnnotation]
		if len(annotation) != 2 {
			return
		}
		flagSDK, setting := annotation[0], annotation[1]
		if flagSDK != selectedSDK {
			visitErr = fmt.Errorf("--%s belongs to SDK %q, not selected SDK %q", flag.Name, flagSDK, selectedSDK)
			return
		}
		value, err := sdkInitFlagValue(cmd.Flags(), flag)
		if err != nil {
			visitErr = fmt.Errorf("read SDK module setting --%s: %w", flag.Name, err)
			return
		}
		settings[setting] = value
	})
	if visitErr != nil {
		return "", visitErr
	}
	if len(settings) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode SDK-module settings: %w", err)
	}
	return string(encoded), nil
}

func cliWorkspacePathContains(parent, child string) bool {
	parent = cliWorkspaceRelPath(parent)
	child = cliWorkspaceRelPath(child)
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}

func cliWorkspaceRelPath(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "/")
	if p == "" {
		return "."
	}
	return p
}
