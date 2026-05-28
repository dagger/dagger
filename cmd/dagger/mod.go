package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var modCmd = &cobra.Command{
	Use:     "mod",
	Short:   "Work with modules in your workspace",
	GroupID: moduleGroup.ID,
}

var modInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Short:   "Install a module into the workspace",
	Long:    "Install a module into the current workspace. Alias for `dagger install`.",
	Example: "dagger mod install github.com/shykes/daggerverse/hello@v0.3.0",
	Args:    cobra.ExactArgs(1),
	RunE:    runWorkspaceInstall,
}

var modUninstallCmd = &cobra.Command{
	Use:     "uninstall [options] <module>",
	Short:   "Uninstall a module from the workspace",
	Long:    "Uninstall a module from the current workspace. Alias for `dagger uninstall`.",
	Example: "dagger mod uninstall hello",
	Args:    cobra.ExactArgs(1),
	RunE:    runWorkspaceUninstall,
}

var modListCmd = &cobra.Command{
	Use:   "list",
	Short: "List modules installed in the workspace",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return listWorkspaceModules(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

var modSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for modules you can install",
	Long: `Search the module registry by name or description.

With no query, lists all known modules.`,
	Example: "dagger mod search wolfi",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) == 1 {
			query = args[0]
		}
		mods, err := loadModuleRegistry()
		if err != nil {
			return err
		}
		return printModuleSearchResults(cmd.OutOrStdout(), searchModuleRegistry(mods, query))
	},
}

func init() {
	modCmd.AddCommand(modInstallCmd, modUninstallCmd, modListCmd, modSearchCmd)

	addWorkspaceInstallFlags(modInstallCmd)
	addWorkspaceHereFlag(modUninstallCmd)

	setWorkspaceFlagPolicy(modInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modUninstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modListCmd, workspaceFlagPolicyLocalOnly)
}

func listWorkspaceModules(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	var res struct {
		CurrentWorkspace struct {
			ModuleList []struct {
				Name       string
				Source     string
				Entrypoint bool
			}
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { moduleList { name source entrypoint } } }`,
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return err
	}

	mods := res.CurrentWorkspace.ModuleList
	if len(mods) == 0 {
		_, err := fmt.Fprintln(out, "No modules installed in the workspace.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSOURCE\tENTRYPOINT"); err != nil {
		return err
	}
	for _, m := range mods {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%t\n", m.Name, m.Source, m.Entrypoint); err != nil {
			return err
		}
	}
	return w.Flush()
}

// registryModule is one entry in the searchable module registry.
type registryModule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
}

// embeddedModuleRegistry is the registry baked in at build time.
//
//go:embed modules.json
var embeddedModuleRegistry []byte

// loadModuleRegistry returns the embedded module registry.
func loadModuleRegistry() ([]registryModule, error) {
	return parseModuleRegistry(embeddedModuleRegistry)
}

func parseModuleRegistry(data []byte) ([]registryModule, error) {
	var mods []registryModule
	if err := json.Unmarshal(data, &mods); err != nil {
		return nil, fmt.Errorf("parse module registry: %w", err)
	}
	return mods, nil
}

// searchModuleRegistry returns modules whose name or description match query
// (case-insensitive substring), sorted by name. An empty query returns all.
func searchModuleRegistry(mods []registryModule, query string) []registryModule {
	out := make([]registryModule, 0, len(mods))
	q := strings.ToLower(query)
	for _, m := range mods {
		if q == "" ||
			strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Description), q) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func printModuleSearchResults(out io.Writer, mods []registryModule) error {
	if len(mods) == 0 {
		_, err := fmt.Fprintln(out, "No matching modules found.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tREPO"); err != nil {
		return err
	}
	for _, m := range mods {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", m.Name, m.Description, m.Repo); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintln(out, "\nRun 'dagger install <repo>' to install a module.")
	return err
}
