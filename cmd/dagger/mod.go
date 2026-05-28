package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

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
		mods, err := loadModuleRegistry(cmd.Context())
		if err != nil {
			return err
		}
		return printModuleSearchResults(cmd.OutOrStdout(), searchModuleRegistry(mods, query))
	},
}

func init() {
	modCmd.AddCommand(modInstallCmd, modUninstallCmd, modListCmd, modSearchCmd, modDepsCmd, modEngineCmd)

	addWorkspaceInstallFlags(modInstallCmd)
	addWorkspaceHereFlag(modUninstallCmd)

	setWorkspaceFlagPolicy(modInstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modUninstallCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(modListCmd, workspaceFlagPolicyLocalOnly)

	modDepsCmd.AddCommand(modDepsAddCmd, modDepsRmCmd, modDepsListCmd)
	modEngineCmd.AddCommand(modEngineRequiredCmd, modEngineRequireCmd, modEngineRequireLatestCmd, modEngineRequireCurrentCmd)

	// These operate on a single module's dagger.json; --mod selects which module
	// (defaults to the current directory).
	modDepsCmd.PersistentFlags().StringVarP(&moduleURL, "mod", "m", "", "Module to edit, a local path or remote git repo (defaults to current directory)")
	modEngineCmd.PersistentFlags().StringVarP(&moduleURL, "mod", "m", "", "Module to edit, a local path or remote git repo (defaults to current directory)")
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

// moduleRegistryURL is the raw location of the searchable module registry. The
// source lives in this repo at cmd/dagger/modules.json and is fetched at
// runtime so it can be updated without rebuilding the CLI.
//
// TODO: point this at dagger/dagger@main once this branch is merged.
const moduleRegistryURL = "https://raw.githubusercontent.com/kpenfound/dagger/refs/heads/dagger_mod/cmd/dagger/modules.json"

// embeddedModuleRegistry is the registry baked in at build time, used as a
// fallback so search keeps working when the remote fetch fails (offline,
// rate-limited, etc.).
//
//go:embed modules.json
var embeddedModuleRegistry []byte

// loadModuleRegistry returns the searchable module registry. It fetches the
// latest copy from moduleRegistryURL, falling back to the embedded registry if
// the fetch (or its parse) fails — so a transient network error or a GitHub
// 429 never breaks `dagger mod search`.
func loadModuleRegistry(ctx context.Context) ([]registryModule, error) {
	if mods, err := fetchModuleRegistry(ctx); err == nil {
		return mods, nil
	}
	return parseModuleRegistry(embeddedModuleRegistry)
}

func fetchModuleRegistry(ctx context.Context) ([]registryModule, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, moduleRegistryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build module registry request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch module registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch module registry: unexpected status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read module registry: %w", err)
	}

	return parseModuleRegistry(data)
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

// --- dagger mod deps: manage a module's own dependencies (dagger.json) ---

var modDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage the current module's dependencies",
}

var modDepsAddCmd = &cobra.Command{
	Use:     "add <module>...",
	Short:   "Add one or more dependencies to the module",
	Example: "dagger mod deps add github.com/dagger/dagger/modules/wolfi",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
			if err != nil {
				return err
			}

			deps := make([]*dagger.ModuleSource, len(args))
			for i, ref := range args {
				deps[i] = dag.ModuleSource(ref)
			}

			if _, err := modSrc.WithDependencies(deps).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
				return fmt.Errorf("add dependencies: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Added dependencies: %s\nRun 'dagger generate' to refresh generated bindings.\n",
				strings.Join(args, ", "))
			return err
		})
	},
}

var modDepsRmCmd = &cobra.Command{
	Use:     "rm <name>...",
	Short:   "Remove one or more dependencies from the module",
	Example: "dagger mod deps rm wolfi",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
			if err != nil {
				return err
			}

			if _, err := modSrc.WithoutDependencies(args).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
				return fmt.Errorf("remove dependencies: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Removed dependencies: %s\nRun 'dagger generate' to refresh generated bindings.\n",
				strings.Join(args, ", "))
			return err
		})
	},
}

var modDepsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the current module's dependencies",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return listModuleDependencies(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

func listModuleDependencies(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	ref, err := getModuleSourceRefWithDefault()
	if err != nil {
		return err
	}

	var res struct {
		ModuleSource struct {
			Dependencies []struct {
				Name   string
				Source string
				Pin    string
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($ref: String!) { moduleSource(refString: $ref) { dependencies { name: moduleName source: asString pin } } }`,
		Variables: map[string]any{"ref": ref},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return err
	}

	deps := res.ModuleSource.Dependencies
	if len(deps) == 0 {
		_, err := fmt.Fprintln(out, "No dependencies.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSOURCE\tPIN"); err != nil {
		return err
	}
	for _, d := range deps {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", d.Name, d.Source, d.Pin); err != nil {
			return err
		}
	}
	return w.Flush()
}

// --- dagger mod engine: manage a module's required engine version ---

var modEngineCmd = &cobra.Command{
	Use:   "engine",
	Short: "Manage the module's required engine version",
}

var modEngineRequiredCmd = &cobra.Command{
	Use:   "required",
	Short: "Print the module's required engine version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ref, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			version, err := dag.ModuleSource(ref).EngineVersion(ctx)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		})
	},
}

var modEngineRequireCmd = &cobra.Command{
	Use:     "require <version>",
	Short:   "Set the module's required engine version",
	Example: "dagger mod engine require v0.21.0",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), engineClient.Dagger(), args[0])
		})
	},
}

var modEngineRequireLatestCmd = &cobra.Command{
	Use:   "require-latest",
	Short: "Set the module's required engine version to the latest released version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		latest, err := latestVersion(cmd.Context())
		if err != nil {
			return fmt.Errorf("determine latest released version: %w", err)
		}
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), engineClient.Dagger(), latest)
		})
	},
}

var modEngineRequireCurrentCmd = &cobra.Command{
	Use:   "require-current",
	Short: "Set the module's required engine version to the currently running engine version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			current, err := dag.Version(ctx)
			if err != nil {
				return fmt.Errorf("determine current engine version: %w", err)
			}
			return setModuleEngineVersion(ctx, cmd.OutOrStdout(), dag, current)
		})
	},
}

func setModuleEngineVersion(ctx context.Context, out io.Writer, dag *dagger.Client, version string) error {
	modSrc, contextDir, err := currentModuleSourceForEdit(ctx, dag)
	if err != nil {
		return err
	}

	// UpdatedConfigDirectory writes dagger.json without running codegen and
	// without validating the engine version against the running engine, so a
	// requirement newer than the engine we're running can be declared.
	if _, err := modSrc.WithEngineVersion(version).UpdatedConfigDirectory().Export(ctx, contextDir); err != nil {
		return fmt.Errorf("set engine version: %w", err)
	}

	_, err = fmt.Fprintf(out, "Set required engine version to %s\n", version)
	return err
}

// currentModuleSourceForEdit resolves the local module selected by --mod (or
// the current directory) for in-place edits to its dagger.json, returning the
// source and the host context directory to export the result back to.
func currentModuleSourceForEdit(ctx context.Context, dag *dagger.Client) (*dagger.ModuleSource, string, error) {
	ref, err := getModuleSourceRefWithDefault()
	if err != nil {
		return nil, "", err
	}

	modSrc := dag.ModuleSource(ref)
	exists, err := modSrc.ConfigExists(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("load module %q: %w", ref, err)
	}
	if !exists {
		return nil, "", fmt.Errorf("no dagger.json found for module %q", ref)
	}

	contextDir, err := modSrc.LocalContextDirectoryPath(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("module %q must be a local module to edit: %w", ref, err)
	}
	return modSrc, contextDir, nil
}
