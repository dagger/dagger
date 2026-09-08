package daggercmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Inspect and configure SDKs",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var sdkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known SDKs",
	Args:  cobra.NoArgs,
	RunE:  runSDKList,
}

var sdkScopeCmd = &cobra.Command{
	Use:   "scope",
	Short: "Inspect and configure SDK scopes",
	Long:  "Inspect and configure SDK scopes. Field edits update dagger.toml only. They do not generate files.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var sdkScopeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SDK scopes",
	Args:  cobra.NoArgs,
	RunE:  runSDKScopeList,
}

var sdkScopeIsModuleCmd = newSDKScopeFieldCommand(
	"is-module [BOOL]",
	"Get or set whether an SDK scope contains a module",
	"is-module",
)

var sdkScopeNameCmd = newSDKScopeFieldCommand(
	"name [NAME]",
	"Get or set an SDK scope name",
	"name",
)

var sdkScopeSDKCmd = newSDKScopeFieldCommand(
	"sdk [SDK]",
	"Get or set the SDK that owns a scope",
	"sdk",
)

func init() {
	sdkScopeCmd.PersistentFlags().String("path", "", "Select a scope by path instead of the workspace CWD")
	sdkScopeListCmd.Flags().Bool("is-module", false, "Filter by whether the scope contains a module")
	sdkScopeListCmd.Flags().String("name", "", "Filter by scope name")
	sdkScopeListCmd.Flags().String("sdk", "", "Filter by SDK name")

	sdkScopeCmd.AddCommand(
		sdkScopeListCmd,
		sdkScopeIsModuleCmd,
		sdkScopeNameCmd,
		sdkScopeSDKCmd,
	)
	sdkCmd.AddCommand(sdkListCmd, sdkScopeCmd)
}

func newSDKScopeFieldCommand(use, short, field string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKScopeField(cmd, field, args)
		},
	}
	cmd.Flags().BoolP("unset", "u", false, "Remove the value")
	return cmd
}

type sdkWorkspaceConfig struct {
	data       []byte
	config     *workspace.Config
	configFile string
	configDir  string
	cwd        string
}

func loadSDKWorkspaceConfig(ctx context.Context, ws *dagger.Workspace, required bool) (*sdkWorkspaceConfig, error) {
	configFile, err := ws.ConfigFile(ctx)
	if err != nil {
		return nil, fmt.Errorf("load workspace config file: %w", err)
	}
	if configFile == "" {
		if required {
			return nil, fmt.Errorf("no dagger.toml found in workspace")
		}
		return nil, nil
	}

	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return nil, fmt.Errorf("load workspace cwd: %w", err)
	}
	rootConfigFile, err := workspaceConfigRootPathFromCwd(configFile, cwd)
	if err != nil {
		return nil, err
	}
	data, err := ws.File(configFile).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read workspace config file: %w", err)
	}
	cfg, err := workspace.ParseConfig([]byte(data))
	if err != nil {
		return nil, err
	}
	return &sdkWorkspaceConfig{
		data:       []byte(data),
		config:     cfg,
		configFile: configFile,
		configDir:  filepath.Dir(rootConfigFile),
		cwd:        cliWorkspaceRelPath(cwd),
	}, nil
}

func withSDKWorkspaceConfig(
	cmd *cobra.Command,
	required bool,
	fn func(context.Context, *dagger.Workspace, *sdkWorkspaceConfig) error,
) error {
	if workspaceEnv != "" {
		return fmt.Errorf("sdk commands do not support --env; SDKs and scopes live in the base workspace config")
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		ws := ec.Dagger().CurrentWorkspace()
		state, err := loadSDKWorkspaceConfig(ctx, ws, required)
		if err != nil {
			return err
		}
		return fn(ctx, ws, state)
	})
}

func runSDKList(cmd *cobra.Command, _ []string) error {
	return withSDKWorkspaceConfig(cmd, false, func(_ context.Context, _ *dagger.Workspace, state *sdkWorkspaceConfig) error {
		if state == nil {
			return nil
		}
		return printSDKList(cmd.OutOrStdout(), state.config)
	})
}

func printSDKList(out io.Writer, cfg *workspace.Config) error {
	if cfg == nil || len(cfg.SDKs) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.SDKs))
	for name := range cfg.SDKs {
		names = append(names, name)
	}
	sort.Strings(names)

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "SDK\tSOURCE"); err != nil {
		return err
	}
	for _, name := range names {
		provider := cfg.Modules[cfg.SDKs[name].Module]
		if _, err := fmt.Fprintf(w, "%s\t%s\n", name, sdkModuleEntrySource(provider)); err != nil {
			return err
		}
	}
	return w.Flush()
}

type sdkScopeRecord struct {
	path       string
	sdk        string
	configPath string
	scope      workspace.SDKScope
}

type sdkScopeFilters struct {
	isModule    bool
	isModuleSet bool
	name        string
	nameSet     bool
	sdk         string
	sdkSet      bool
}

func runSDKScopeList(cmd *cobra.Command, _ []string) error {
	if cmd.Flag("path").Changed {
		return fmt.Errorf("--path is not supported for %q", cmd.CommandPath())
	}
	filters := sdkScopeFilters{}
	var err error
	filters.isModule, err = cmd.Flags().GetBool("is-module")
	if err != nil {
		return err
	}
	filters.isModuleSet = cmd.Flags().Changed("is-module")
	filters.name, err = cmd.Flags().GetString("name")
	if err != nil {
		return err
	}
	filters.nameSet = cmd.Flags().Changed("name")
	filters.sdk, err = cmd.Flags().GetString("sdk")
	if err != nil {
		return err
	}
	filters.sdkSet = cmd.Flags().Changed("sdk")

	return withSDKWorkspaceConfig(cmd, false, func(_ context.Context, _ *dagger.Workspace, state *sdkWorkspaceConfig) error {
		if state == nil {
			return nil
		}
		records, err := sdkScopeRecords(state.config, state.configDir)
		if err != nil {
			return err
		}
		return printSDKScopeList(cmd.OutOrStdout(), filterSDKScopeRecords(records, filters))
	})
}

func sdkScopeRecords(cfg *workspace.Config, configDir string) ([]sdkScopeRecord, error) {
	if cfg == nil {
		return nil, nil
	}
	var records []sdkScopeRecord
	for sdkName, entry := range cfg.SDKs {
		for configPath, scope := range entry.Scopes {
			workspacePath, err := workspace.ResolveSDKManagedPath(configDir, configPath)
			if err != nil {
				return nil, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			records = append(records, sdkScopeRecord{
				path:       cliWorkspaceRelPath(workspacePath),
				sdk:        sdkName,
				configPath: configPath,
				scope:      scope,
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].path != records[j].path {
			return records[i].path < records[j].path
		}
		return records[i].sdk < records[j].sdk
	})
	return records, nil
}

func filterSDKScopeRecords(records []sdkScopeRecord, filters sdkScopeFilters) []sdkScopeRecord {
	filtered := make([]sdkScopeRecord, 0, len(records))
	for _, record := range records {
		if filters.isModuleSet && record.scope.IsModule != filters.isModule {
			continue
		}
		if filters.nameSet && record.scope.Name != filters.name {
			continue
		}
		if filters.sdkSet && record.sdk != filters.sdk {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func printSDKScopeList(out io.Writer, records []sdkScopeRecord) error {
	if len(records) == 0 {
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tPATH\tSDK\tIS-MODULE"); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", record.scope.Name, record.path, record.sdk, record.scope.IsModule); err != nil {
			return err
		}
	}
	return w.Flush()
}

func runSDKScopeField(cmd *cobra.Command, field string, args []string) error {
	unset, err := cmd.Flags().GetBool("unset")
	if err != nil {
		return err
	}
	if unset && len(args) != 0 {
		return fmt.Errorf("--unset does not accept a value")
	}
	path, err := cmd.Flags().GetString("path")
	if err != nil {
		return err
	}
	if cmd.Flag("path").Changed && path == "" {
		return fmt.Errorf("--path must not be empty")
	}

	return withSDKWorkspaceConfig(cmd, true, func(ctx context.Context, ws *dagger.Workspace, state *sdkWorkspaceConfig) error {
		record, err := selectSDKScopeRecord(state, path)
		if err != nil {
			return err
		}
		if !unset && len(args) == 0 {
			return printSDKScopeField(cmd.OutOrStdout(), record, field)
		}
		if err := updateSDKScopeField(state.config, record, field, args, unset); err != nil {
			return err
		}
		updated, err := workspace.UpdateConfigBytes(state.data, state.config)
		if err != nil {
			return err
		}
		return ws.WithNewFile(state.configFile, string(updated)).Export(ctx)
	})
}

func selectSDKScopeRecord(state *sdkWorkspaceConfig, requestedPath string) (sdkScopeRecord, error) {
	records, err := sdkScopeRecords(state.config, state.configDir)
	if err != nil {
		return sdkScopeRecord{}, err
	}
	targetPath := state.cwd
	exact := requestedPath != ""
	if exact {
		targetPath, err = workspace.ResolveSDKManagedPath(state.cwd, requestedPath)
		if err != nil {
			return sdkScopeRecord{}, err
		}
		targetPath = cliWorkspaceRelPath(targetPath)
	}

	var matches []sdkScopeRecord
	for _, record := range records {
		if exact {
			if record.path == targetPath {
				matches = append(matches, record)
			}
			continue
		}
		if cliWorkspacePathContains(record.path, targetPath) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		if exact {
			return sdkScopeRecord{}, fmt.Errorf("no SDK scope exists at path %q", targetPath)
		}
		return sdkScopeRecord{}, fmt.Errorf("no SDK scope contains workspace cwd %q", targetPath)
	}
	if !exact {
		sort.Slice(matches, func(i, j int) bool { return len(matches[i].path) > len(matches[j].path) })
	}
	if len(matches) > 1 && matches[0].path == matches[1].path {
		return sdkScopeRecord{}, fmt.Errorf("path %q belongs to multiple SDK scopes", matches[0].path)
	}
	return matches[0], nil
}

func printSDKScopeField(out io.Writer, record sdkScopeRecord, field string) error {
	var value string
	switch field {
	case "is-module":
		value = strconv.FormatBool(record.scope.IsModule)
	case "name":
		value = record.scope.Name
	case "sdk":
		value = record.sdk
	default:
		return fmt.Errorf("unknown SDK scope field %q", field)
	}
	_, err := fmt.Fprintln(out, value)
	return err
}

func updateSDKScopeField(cfg *workspace.Config, record sdkScopeRecord, field string, args []string, unset bool) error {
	entry := cfg.SDKs[record.sdk]
	scope := entry.Scopes[record.configPath]

	switch field {
	case "is-module":
		if unset {
			scope.IsModule = false
		} else {
			value, err := strconv.ParseBool(args[0])
			if err != nil {
				return fmt.Errorf("invalid BOOL %q: %w", args[0], err)
			}
			scope.IsModule = value
		}
	case "name":
		if unset {
			scope.Name = ""
		} else {
			scope.Name = args[0]
		}
	case "sdk":
		if unset {
			delete(entry.Scopes, record.configPath)
			if len(entry.Scopes) == 0 {
				entry.Scopes = nil
			}
			cfg.SDKs[record.sdk] = entry
			return nil
		}
		targetName := args[0]
		target, ok := cfg.SDKs[targetName]
		if !ok {
			return fmt.Errorf("SDK %q is not known; use `dagger sdk list`", targetName)
		}
		if targetName == record.sdk {
			return nil
		}
		if _, exists := target.Scopes[record.configPath]; exists {
			return fmt.Errorf("SDK %q already owns a scope at path %q", targetName, record.path)
		}
		delete(entry.Scopes, record.configPath)
		if len(entry.Scopes) == 0 {
			entry.Scopes = nil
		}
		cfg.SDKs[record.sdk] = entry
		if target.Scopes == nil {
			target.Scopes = map[string]workspace.SDKScope{}
		}
		target.Scopes[record.configPath] = scope
		cfg.SDKs[targetName] = target
		return nil
	default:
		return fmt.Errorf("unknown SDK scope field %q", field)
	}
	if scope.IsModule && scope.Name == "" {
		return fmt.Errorf("scope name is required when is-module is true")
	}

	if sdkScopeIsEmpty(scope) {
		delete(entry.Scopes, record.configPath)
		if len(entry.Scopes) == 0 {
			entry.Scopes = nil
		}
	} else {
		entry.Scopes[record.configPath] = scope
	}
	cfg.SDKs[record.sdk] = entry
	return nil
}

func sdkScopeIsEmpty(scope workspace.SDKScope) bool {
	return !scope.IsModule && scope.Name == "" && len(scope.Clients) == 0 && len(scope.Settings) == 0
}
