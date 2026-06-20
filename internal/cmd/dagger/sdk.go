package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// `dagger sdk` is the SDK-management group: install (alias-aware), uninstall
// (refuse-if-authored), list, search the registry, and inspect a given SDK's
// init flags. An install becomes an SDK when added through this group — the
// engine writes a `[modules.<name>.as-sdk]` marker, optionally with a
// user-facing alias name that `dagger module init <sdk>` /
// `dagger api client init <sdk>` use to dispatch.
//
// The boundary with `dagger module` is: SDK is the tool, module is the thing
// the SDK creates. `dagger sdk install` adds the SDK; `dagger module init
// <sdk> <name>` uses an installed SDK to create a module.
var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Install and manage SDKs (the modules that author other modules)",
	Long: `Install and manage SDKs.

SDKs are workspace modules whose role is to scaffold/codegen other things:
new Dagger modules (` + "`dagger module init`" + `) or typed clients against the
Dagger API (` + "`dagger api client init`" + `). An install becomes an SDK when
added through this group — ` + "`dagger sdk install go`" + ` installs
[modules.go-sdk] with [modules.go-sdk.as-sdk] name = "go" so
` + "`dagger module init go`" + ` / ` + "`dagger api client init go`" + `
dispatch through that SDK.`,
}

var (
	sdkInstallName string
	sdkInstallHere bool

	sdkUninstallForce bool
	sdkUninstallHere  bool
)

var sdkInstallCmd = &cobra.Command{
	Use:   "install [options] <name-or-ref>",
	Short: "Install an SDK and mark it",
	Long: `Install an SDK into the current workspace and mark it with the
[modules.<name>.as-sdk] table.

Alias resolution: ` + "`dagger sdk install go`" + ` resolves "go" via the
embedded sdks.json registry to github.com/dagger/go-sdk. The workspace
install name is the canonical ref basename (` + "`go-sdk`" + `), and the
user-facing name is persisted as [modules.go-sdk.as-sdk] name = "go".
Direct refs work too:
` + "`dagger sdk install github.com/foo/sdk`" + ` is installed as
[modules.sdk] by basename.

Generic ` + "`dagger install <ref>`" + ` does NOT mark anything as an SDK.
The marker is opt-in via this verb.`,
	Example: "dagger sdk install go",
	Args:    cobra.ExactArgs(1),
	RunE:    runSDKInstall,
}

var sdkUninstallCmd = &cobra.Command{
	Use:   "uninstall [options] <name>",
	Short: "Remove an SDK install",
	Long: `Remove an SDK install from the current workspace.

Refuses if anything is authored under the SDK (entries in
[[modules.<name>.as-sdk.modules]] or [[modules.<name>.as-sdk.clients]]).
Pass --force to override and remove anyway; the authored module/client
files are left on disk untouched, only the workspace entries go away.`,
	Example: "dagger sdk uninstall go",
	Args:    cobra.ExactArgs(1),
	RunE:    runSDKUninstall,
}

var sdkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed SDKs",
	Long: `List installs in the current workspace that carry the
[modules.<name>.as-sdk] marker.

The M and C columns count workspace-local modules and clients authored
under each SDK (the entries in [[modules.<name>.as-sdk.modules]] and
[[modules.<name>.as-sdk.clients]]).`,
	Args: cobra.NoArgs,
	RunE: runSDKList,
}

var sdkSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Discover SDKs in the SDK registry",
	Long: `List entries in the embedded SDK registry (sdks.json).

With no query, prints all known SDKs and their aliases. With a query,
filters by case-insensitive substring on name, description, alias, or repo.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSDKSearch,
}

var sdkModuleOptionsCmd = &cobra.Command{
	Use:   "module-options <sdk>",
	Short: "Show SDK-specific flags accepted by `dagger module init <sdk>`",
	Long: `Print the SDK-specific flags ` + "`dagger module init <sdk> <name>`" + `
accepts, introspected from the SDK's initModule function.

Requires the SDK to implement the initModule capability.`,
	Args: cobra.ExactArgs(1),
	RunE: runSDKModuleOptions,
}

var sdkClientOptionsCmd = &cobra.Command{
	Use:   "client-options <sdk>",
	Short: "Show SDK-specific flags accepted by `dagger api client init <sdk>`",
	Long: `Print the SDK-specific flags ` + "`dagger api client init <sdk>`" + `
accepts, introspected from the SDK's initClient function.

Requires the SDK to implement the initClient capability.`,
	Args: cobra.ExactArgs(1),
	RunE: runSDKClientOptions,
}

func init() {
	sdkInstallCmd.Flags().StringVarP(&sdkInstallName, "name", "n", "", "Override the workspace install name (defaults to the registry repo basename, or the basename of a direct ref)")
	sdkInstallCmd.Flags().BoolVar(&sdkInstallHere, "here", false, "Write to the workspace config directory at the workspace cwd")

	sdkUninstallCmd.Flags().BoolVar(&sdkUninstallForce, "force", false, "Remove even if modules or clients are authored under this SDK")
	sdkUninstallCmd.Flags().BoolVar(&sdkUninstallHere, "here", false, "Write to the workspace config directory at the workspace cwd")

	sdkCmd.AddCommand(
		sdkInstallCmd,
		sdkUninstallCmd,
		sdkListCmd,
		sdkSearchCmd,
		sdkModuleOptionsCmd,
		sdkClientOptionsCmd,
	)

	// These mutate workspace config on the host; rejecting `--workspace` for
	// anything other than a local path keeps remote refs from sneaking into
	// install/uninstall paths (matches `dagger install` / `dagger uninstall`).
	setWorkspaceFlagPolicy(sdkInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(sdkUninstallCmd, workspaceFlagPolicyLocalOnly)
}

func runSDKInstall(cmd *cobra.Command, args []string) error {
	input := args[0]
	canonicalRef, defaultName, asSDKName, err := sdkResolveInstall(input)
	if err != nil {
		return err
	}
	name := sdkInstallName
	if name == "" {
		name = defaultName
	}

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return callSDKInstall(ctx, ec.Dagger(), cmd.OutOrStdout(), canonicalRef, name, asSDKName, sdkInstallHere)
	})
}

// callSDKInstall invokes Workspace.install with asSdk=true via raw GraphQL.
// Will collapse to `dag.CurrentWorkspace().Install(ctx, ref,
// WorkspaceInstallOpts{Name, Here, AsSdk: true, AsSdkName: ...})` once the Go
// SDK binding regenerates against the new schema.
func callSDKInstall(ctx context.Context, dag *dagger.Client, out io.Writer, ref, name, asSDKName string, here bool) error {
	var res struct {
		CurrentWorkspace struct {
			Install string `json:"install"`
		} `json:"currentWorkspace"`
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query SDKInstall($ref: String!, $name: String, $here: Boolean, $asSdk: Boolean, $asSdkName: String) {
  currentWorkspace {
    install(ref: $ref, name: $name, here: $here, asSdk: $asSdk, asSdkName: $asSdkName)
  }
}`,
		Variables: map[string]any{
			"ref":       ref,
			"name":      name,
			"here":      here,
			"asSdk":     true,
			"asSdkName": asSDKName,
		},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return fmt.Errorf("install sdk: %w", err)
	}
	_, err = fmt.Fprintln(out, res.CurrentWorkspace.Install)
	return err
}

func runSDKUninstall(cmd *cobra.Command, args []string) error {
	input := args[0]

	// Refuse-if-authored is a CLI-side check. It runs against the on-disk
	// workspace config, not the engine — there's no need to bootstrap a
	// session just to read TOML, and the check has to happen before we
	// dispatch the uninstall mutation.
	cfg, cfgPath, err := readLocalWorkspaceConfig()
	if err != nil {
		return err
	}
	sdk, err := resolveConfiguredSDK(cfg, input)
	if err != nil {
		if entry, ok := cfg.Modules[input]; ok && entry.AsSDK == nil {
			return fmt.Errorf("%q is installed in %s but is not marked as an SDK; use `dagger uninstall %s` instead", input, cfgPath, input)
		}
		return err
	}
	name := sdk.moduleName
	entry := sdk.entry
	if !sdkUninstallForce {
		nMods := len(entry.AsSDK.Modules)
		nClients := len(entry.AsSDK.Clients)
		if nMods+nClients > 0 {
			return fmt.Errorf("%q has %d module(s) and %d client(s) authored under it (see %s); pass --force to remove the SDK entry anyway (files on disk are left untouched)", name, nMods, nClients, cfgPath)
		}
	}

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return uninstallWorkspaceModule(ctx, cmd.OutOrStdout(), ec.Dagger(), name, sdkUninstallHere)
	})
}

func runSDKList(cmd *cobra.Command, _ []string) error {
	cfg, _, err := readLocalWorkspaceConfig()
	if err != nil {
		return err
	}
	type row struct {
		name, alias, source string
		modules, clients    int
	}
	var rows []row
	for name, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		rows = append(rows, row{
			name:    name,
			alias:   sdkCommandName(name, entry),
			source:  entry.Source,
			modules: len(entry.AsSDK.Modules),
			clients: len(entry.AsSDK.Clients),
		})
	}
	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No SDKs installed in this workspace. Try `dagger sdk install go` (or another SDK from `dagger sdk search`).")
		return err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	// M = authored modules, C = generated clients. Cheap capability
	// affordance until full per-SDK introspection lands with task #129.
	if _, err := fmt.Fprintln(w, "NAME\tALIAS\tSOURCE\tM\tC"); err != nil {
		return err
	}
	for _, r := range rows {
		alias := "-"
		if r.alias != r.name {
			alias = r.alias
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n", r.name, alias, r.source, r.modules, r.clients); err != nil {
			return err
		}
	}
	return w.Flush()
}

func runSDKSearch(cmd *cobra.Command, args []string) error {
	query := ""
	if len(args) == 1 {
		query = args[0]
	}
	entries, err := loadSDKRegistry()
	if err != nil {
		return err
	}
	return printSDKSearchResults(cmd.OutOrStdout(), searchSDKRegistry(entries, query))
}

func searchSDKRegistry(entries []sdkEntry, query string) []sdkEntry {
	q := strings.ToLower(query)
	matched := make([]sdkEntry, 0, len(entries))
	for _, e := range entries {
		if q == "" {
			matched = append(matched, e)
			continue
		}
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) ||
			strings.Contains(strings.ToLower(e.Repo), q) {
			matched = append(matched, e)
			continue
		}
		for _, alias := range e.Aliases {
			if strings.Contains(strings.ToLower(alias), q) {
				matched = append(matched, e)
				break
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	return matched
}

func printSDKSearchResults(out io.Writer, entries []sdkEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintln(out, "No SDKs match.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tALIASES\tREPO"); err != nil {
		return err
	}
	for _, e := range entries {
		aliases := "-"
		if len(e.Aliases) > 0 {
			aliases = strings.Join(e.Aliases, ",")
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.Description, aliases, e.Repo); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintln(out, "\nRun 'dagger sdk install <NAME>' to install an SDK.")
	return err
}

func runSDKModuleOptions(cmd *cobra.Command, args []string) error {
	return runSDKInitOptions(cmd, args[0], sdkInitKindModule)
}

func runSDKClientOptions(cmd *cobra.Command, args []string) error {
	return runSDKInitOptions(cmd, args[0], sdkInitKindClient)
}

func runSDKInitOptions(cmd *cobra.Command, sdkName string, kind sdkInitKind) error {
	cfg, cfgPath, err := readLocalWorkspaceConfig()
	if err != nil {
		return err
	}
	sdk, err := resolveConfiguredSDK(cfg, sdkName)
	if err != nil {
		return err
	}
	sdkRef, err := sdkInitModuleEntrySource(sdk.entry, filepath.Dir(cfgPath))
	if err != nil {
		return err
	}

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		fn, err := inspectSDKInitFunction(ctx, ec.Dagger(), sdkRef, kind)
		if errors.Is(err, errSDKInitFunctionNotFound) {
			return fmt.Errorf("%q does not support %s", sdk.commandName, sdkInitCapabilityName(kind))
		}
		if err != nil {
			return err
		}
		args, err := sdkInitFunctionFlagArgs(fn, kind)
		if err != nil {
			return err
		}
		return printSDKInitOptions(cmd.OutOrStdout(), sdk.commandName, kind, args)
	})
}

func sdkInitCapabilityName(kind sdkInitKind) string {
	if kind == sdkInitKindClient {
		return "client init"
	}
	return "module init"
}

func printSDKInitOptions(out io.Writer, sdkName string, kind sdkInitKind, args []*modFunctionArg) error {
	usage := fmt.Sprintf("dagger module init %s <name>", sdkName)
	if kind == sdkInitKindClient {
		usage = fmt.Sprintf("dagger api client init %s <path> <module>", sdkName)
	}
	if len(args) == 0 {
		_, err := fmt.Fprintf(out, "No SDK-specific flags for `%s`.\n", usage)
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(w, "Flags for `%s`:\n\n", usage); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "FLAG\tTYPE\tREQUIRED\tDESCRIPTION"); err != nil {
		return err
	}
	for _, arg := range args {
		required := "no"
		if arg.IsRequired() {
			required = "yes"
		}
		desc := arg.Short()
		if desc == "" {
			desc = "-"
		}
		if _, err := fmt.Fprintf(w, "--%s\t%s\t%s\t%s\n", arg.FlagName(), arg.TypeDef.String(), required, desc); err != nil {
			return err
		}
	}
	return w.Flush()
}

func readLocalWorkspaceConfig() (*workspace.Config, string, error) {
	// Walk up from cwd looking for dagger.toml. Mirrors the lookup
	// `dagger module sdk` uses; consistent behavior means a user who reaches
	// for `dagger sdk list` from a subdirectory sees the same workspace the
	// rest of the CLI does.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getwd: %w", err)
	}
	dir := cwd
	for {
		cfgPath := filepath.Join(dir, workspace.ConfigFileName)
		if _, err := os.Stat(cfgPath); err == nil {
			data, err := os.ReadFile(cfgPath)
			if err != nil {
				return nil, "", fmt.Errorf("read workspace config %q: %w", cfgPath, err)
			}
			cfg, err := workspace.ParseConfig(data)
			if err != nil {
				return nil, "", fmt.Errorf("parse workspace config %q: %w", cfgPath, err)
			}
			return cfg, cfgPath, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", fmt.Errorf("no workspace config (%s) found from %q upward; run `dagger sdk install <sdk>` to create one", workspace.ConfigFileName, cwd)
		}
		dir = parent
	}
}
