package daggercmd

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var searchSDKOnly bool

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for modules you can install",
	Long: `Search the module registry by name or description.

With no query, lists all known modules and SDK modules.`,
	Example: "dagger module search wolfi",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) == 1 {
			query = args[0]
		}
		mods, err := loadSearchRegistry(cmd.Context(), searchSDKOnly)
		if err != nil {
			return err
		}
		return printModuleSearchResults(cmd.OutOrStdout(), searchModuleRegistry(mods, query))
	},
}

func init() {
	searchCmd.Flags().BoolVar(&searchSDKOnly, "sdk", false, "Only show modules that provide SDK capabilities")
}

// loadSearchRegistry returns the catalog entries to search: every entry, or
// only SDKs with sdkOnly.
func loadSearchRegistry(ctx context.Context, sdkOnly bool) ([]registryModule, error) {
	mods, err := loadModuleRegistry(ctx)
	if err != nil {
		return nil, err
	}
	return filterSearchRegistry(withBuiltinModules(mods), sdkOnly), nil
}

func filterSearchRegistry(mods []registryModule, sdkOnly bool) []registryModule {
	if !sdkOnly {
		return mods
	}
	return slices.DeleteFunc(slices.Clone(mods), func(m registryModule) bool {
		return m.Kind != moduleKindSDK
	})
}

// searchModuleRegistry returns modules whose name or description match query
// (case-insensitive substring), sorted by the displayed source. An empty query
// returns all.
func searchModuleRegistry(mods []registryModule, query string) []registryModule {
	out := make([]registryModule, 0, len(mods))
	q := strings.ToLower(query)
	for _, m := range mods {
		if q == "" ||
			strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Description), q) ||
			strings.Contains(strings.ToLower(m.Repo), q) ||
			slices.ContainsFunc(m.Aliases, func(alias string) bool {
				return strings.Contains(strings.ToLower(alias), q)
			}) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func printModuleSearchResults(out io.Writer, mods []registryModule) error {
	if len(mods) == 0 {
		_, err := fmt.Fprintln(out, "No matching modules found.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "SOURCE\tDESCRIPTION"); err != nil {
		return err
	}
	for _, m := range mods {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", m.Repo, m.Description); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintln(out, "\nRun 'dagger install <SOURCE>' to install a module.")
	return err
}
