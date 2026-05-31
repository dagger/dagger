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
	Use:     "module",
	Aliases: []string{"mod"},
	Short:   "Work with modules in your workspace",
	Annotations: map[string]string{
		visibleAliasesAnnotation: "mod",
	},
}

var modInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Short:   "Install a module into the workspace",
	Long:    "Install a module into the current workspace. Alias for `dagger workspace install`.",
	Example: "dagger module install github.com/shykes/daggerverse/hello@v0.3.0",
	Args:    cobra.ExactArgs(1),
	RunE:    runWorkspaceInstall,
}

var modUninstallCmd = &cobra.Command{
	Use:     "uninstall [options] <module>",
	Short:   "Uninstall a module from the workspace",
	Long:    "Uninstall a module from the current workspace. Alias for `dagger workspace uninstall`.",
	Example: "dagger module uninstall hello",
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

var modRecommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Recommend modules based on files in your workspace",
	Long: `Scan the workspace for files matching the recommend glob of each known
module and print those whose pattern matches at least one file.

Modules already installed in the workspace are excluded.`,
	Example: "dagger module recommend",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return runRecommend(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

var modSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for modules you can install",
	Long: `Search the module registry by name or description.

With no query, lists all known modules.`,
	Example: "dagger module search wolfi",
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
	modCmd.AddCommand(modInstallCmd, modUninstallCmd, modListCmd, modSearchCmd, modRecommendCmd)

	addWorkspaceInstallFlags(modInstallCmd)
	addWorkspaceHereFlag(modUninstallCmd)

	// install/uninstall mutate workspace config and only make sense for a
	// local workspace; list/search/recommend are read-only and work for
	// both local and remote -W refs.
	setWorkspaceFlagPolicy(modInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modUninstallCmd, workspaceFlagPolicyLocalOnly)
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
	// Recommend is a doublestar glob (e.g. "**/go.mod") used by
	// `dagger module recommend` to decide whether to suggest this module
	// based on files present in the workspace. Empty means never recommended.
	Recommend string `json:"recommend,omitempty"`
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

// recommendation pairs a registry entry with the workspace-relative path that
// triggered its recommendation.
type recommendation struct {
	Module registryModule
	Match  string
}

// recommendExcludeDirs lists directories we strip from the workspace
// snapshot before globbing. Keeps the work cheap and avoids false positives
// from vendored or generated content. Patterns match Workspace.Directory's
// exclude semantics (path-prefix style).
var recommendExcludeDirs = []string{
	".git/",
	".dagger/",
	"node_modules/",
	"vendor/",
	"dist/",
	"build/",
	"target/",
}

// runRecommend is the cobra runtime for `dagger module recommend`. It works for
// both local and remote workspaces by globbing through the engine rather
// than walking the local filesystem.
func runRecommend(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	ws := dag.CurrentWorkspace()

	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return err
	}

	mods, err := loadModuleRegistry()
	if err != nil {
		return err
	}

	// "/" resolves to the workspace root regardless of cwd; excluding common
	// vendored/generated dirs keeps matches relevant.
	dir := ws.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: recommendExcludeDirs,
	})

	recs := make([]recommendation, 0, len(mods))
	for _, m := range mods {
		if m.Recommend == "" || installed[m.Name] {
			continue
		}
		matches, err := dir.Glob(ctx, m.Recommend)
		if err != nil {
			// A bad pattern in the registry shouldn't take down the whole
			// command; just skip the entry.
			continue
		}
		if len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		recs = append(recs, recommendation{Module: m, Match: matches[0]})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Module.Name < recs[j].Module.Name })

	return printRecommendations(out, recs)
}

// installedModuleNames returns the set of module names installed in the
// current workspace (the same query backing `dagger module list`).
func installedModuleNames(ctx context.Context, dag *dagger.Client) (map[string]bool, error) {
	var res struct {
		CurrentWorkspace struct {
			ModuleList []struct {
				Name string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { moduleList { name } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, fmt.Errorf("list installed modules: %w", err)
	}
	installed := make(map[string]bool, len(res.CurrentWorkspace.ModuleList))
	for _, m := range res.CurrentWorkspace.ModuleList {
		installed[m.Name] = true
	}
	return installed, nil
}

func printRecommendations(out io.Writer, recs []recommendation) error {
	if len(recs) == 0 {
		_, err := fmt.Fprintln(out, "No recommended modules found.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tREPO\tMATCHED"); err != nil {
		return err
	}
	for _, r := range recs {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Module.Name, r.Module.Description, r.Module.Repo, r.Match); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintln(out, "\nRun 'dagger module install <repo>' to install a module.")
	return err
}
