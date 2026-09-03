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
	moduleClientAddSDK    string
	moduleClientScopeSDK  string
	moduleClientListAll   bool
	moduleClientListSDK   string
	moduleClientUpdateAll bool
	moduleClientUpdateSDK string
)

var moduleInitCmd = &cobra.Command{
	Use:                   "init <sdk>",
	Short:                 "Initialize a module in the workspace",
	Long:                  "Initialize a module in the workspace, using the named SDK.",
	Example:               "dagger module init go",
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSDKModuleInit(cmd, args[0])
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
	Use:                   "add <module>",
	Short:                 "Add and generate a module client",
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSDKModuleClientAdd(cmd, args[0])
	},
}

var moduleClientRemoveCmd = &cobra.Command{
	Use:   "rm <module>",
	Short: "Remove a module client",
	Long:  "Remove a module client and regenerate its SDK scope.",
	Args:  cobra.ExactArgs(1),
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
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runSDKModuleClientList(cmd)
	},
}

func init() {
	moduleInitCmd.Flags().StringVarP(&moduleInitName, "name", "n", "", "Module name (inferred when omitted)")
	moduleInitCmd.Flags().StringVar(&moduleInitPath, "path", "", "Module path (default: .dagger/modules/<name> beside dagger.toml)")
	moduleClientAddCmd.Flags().StringVar(&moduleClientAddSDK, "sdk", "", "SDK module to use")
	moduleClientScopeCmd.Flags().StringVar(&moduleClientScopeSDK, "sdk", "", "SDK module to query")
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
	settings, err := sdkModuleSettingsJSON(cmd, sdk)
	if err != nil {
		return err
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleInit($sdk: String!, $name: String, $path: String, $settings: JSON) {
  currentWorkspace {
    result: withInitModule(sdk: $sdk, name: $name, path: $path, settings: $settings) { id }
  }
}`, map[string]any{
		"sdk":      sdk,
		"name":     moduleInitName,
		"path":     moduleInitPath,
		"settings": dagger.JSON(settings),
	}, func() error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), moduleInitCustomPathMessage(moduleInitPath))
		return err
	})
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
	settings, err := sdkModuleSettingsJSON(cmd, moduleClientAddSDK)
	if err != nil {
		return err
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleClientAdd($module: String!, $sdk: String, $settings: JSON) {
  currentWorkspace {
    result: withClient(module: $module, sdk: $sdk, settings: $settings) { id }
  }
}`, map[string]any{
		"module":   module,
		"sdk":      moduleClientAddSDK,
		"settings": dagger.JSON(settings),
	}, nil)
}

func runSDKModuleClientRemove(cmd *cobra.Command, module string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("module client rm does not support --env; SDK scopes live in the base workspace config")
	}
	return mutateSDKModuleWorkspace(cmd, `
query ModuleClientRemove($module: String!) {
  currentWorkspace {
    result: withoutClient(module: $module) { id }
  }
}`, map[string]any{"module": module}, nil)
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
	afterApply func() error,
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

		current := dag.CurrentWorkspace().WithWorkdir(".")
		updated := dagger.Ref[*dagger.Workspace](dag, result.CurrentWorkspace.Result.ID).WithWorkdir(".")
		applied, err := handleWorkspaceResponse(ctx, dag, current, updated, autoApply)
		if err != nil || !applied || afterApply == nil {
			return err
		}
		return afterApply()
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
			Query:     `query ModuleClientScope($sdk: String) { currentWorkspace { detectScope(sdk: $sdk) } }`,
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
		dag := ec.Dagger()
		ws := dag.CurrentWorkspace()
		configFile, err := ws.ConfigFile(ctx)
		if err != nil {
			return err
		}
		if configFile == "" {
			return nil
		}
		cwd, err := ws.Cwd(ctx)
		if err != nil {
			return err
		}
		data, err := ws.ConfigRead(ctx)
		if err != nil {
			return err
		}
		cfg, err := workspace.ParseConfig([]byte(data))
		if err != nil {
			return err
		}
		cwd = cliWorkspaceRelPath(cwd)
		configDir := filepath.Dir(filepath.Clean(filepath.Join(cwd, configFile)))

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
		if selectedSDK == "" {
			visitErr = fmt.Errorf("--%s does not select SDK %q; also pass --sdk=%s", flag.Name, flagSDK, flagSDK)
			return
		}
		if flagSDK != selectedSDK {
			visitErr = fmt.Errorf("--%s belongs to SDK %q, not selected SDK %q", flag.Name, flagSDK, selectedSDK)
			return
		}
		settings[setting] = sdkInitFlagValue(flag)
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
