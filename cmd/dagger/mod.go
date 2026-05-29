package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/bmatcuk/doublestar/v4"
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

var modRecommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Recommend modules based on files in your workspace",
	Long: `Scan the workspace for files matching the recommend glob of each known
module and print those whose pattern matches at least one file.

Modules already installed in the workspace are excluded.`,
	Example: "dagger mod recommend",
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
	modCmd.AddCommand(modInstallCmd, modUninstallCmd, modListCmd, modSearchCmd, modRecommendCmd)

	addWorkspaceInstallFlags(modInstallCmd)
	addWorkspaceHereFlag(modUninstallCmd)

	setWorkspaceFlagPolicy(modInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modUninstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modListCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modRecommendCmd, workspaceFlagPolicyLocalOnly)
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
	// `dagger mod recommend` to decide whether to suggest this module
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

// recommendWalkSkipDirs lists directories we never descend into when scanning
// the workspace for recommend glob matches. Keeps the walk cheap and avoids
// false positives from vendored or generated content.
var recommendWalkSkipDirs = map[string]bool{
	".git":         true,
	".dagger":      true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

// runRecommend is the cobra runtime for `dagger mod recommend`. It
// resolves the workspace root (respecting -W), gathers installed module
// names, walks the workspace, and prints the matches.
func runRecommend(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	ws := dag.CurrentWorkspace()

	address, err := ws.Address(ctx)
	if err != nil {
		return fmt.Errorf("load workspace address: %w", err)
	}
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return fmt.Errorf("load workspace cwd: %w", err)
	}
	root, err := workspaceRootFromAddress(address, cwd)
	if err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("workspace root not available")
	}

	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return err
	}

	mods, err := loadModuleRegistry()
	if err != nil {
		return err
	}

	files, err := collectWorkspaceFiles(root)
	if err != nil {
		return fmt.Errorf("scan workspace %s: %w", root, err)
	}

	return printRecommendations(out, recommendModules(mods, files, installed))
}

// installedModuleNames returns the set of module names installed in the
// current workspace (the same query backing `dagger mod list`).
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

// collectWorkspaceFiles walks root and returns paths relative to it, using
// forward slashes, suitable for doublestar matching. Directories listed in
// recommendWalkSkipDirs are pruned.
func collectWorkspaceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Surface root-level errors; tolerate per-entry permission errors.
			if path == root {
				return err
			}
			if errors.Is(err, fs.ErrPermission) {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if recommendWalkSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// recommendModules is the pure matching core: given the registry, a sorted
// list of workspace-relative file paths, and the set of already-installed
// module names, it returns one recommendation per module whose Recommend
// glob matches at least one file (and which is not already installed).
//
// Results are sorted by module name; the recorded Match is the first matching
// path in the input order.
func recommendModules(mods []registryModule, files []string, installed map[string]bool) []recommendation {
	out := make([]recommendation, 0, len(mods))
	for _, m := range mods {
		if m.Recommend == "" {
			continue
		}
		if installed[m.Name] {
			continue
		}
		for _, f := range files {
			ok, err := doublestar.Match(m.Recommend, f)
			if err != nil {
				// A bad pattern is a registry bug; skip the entry rather
				// than abort the whole command.
				break
			}
			if ok {
				out = append(out, recommendation{Module: m, Match: f})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module.Name < out[j].Module.Name })
	return out
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

	_, err := fmt.Fprintln(out, "\nRun 'dagger mod install <repo>' to install a module.")
	return err
}
