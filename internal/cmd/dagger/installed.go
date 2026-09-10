package daggercmd

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// installedCmd lists installed modules in the current workspace.
var installedCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed modules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return listWorkspaceModules(ctx, cmd.OutOrStdout(), engineClient.Dagger())
		})
	},
}

type moduleListRow struct {
	Name   string
	Source string
}

func listWorkspaceModules(ctx context.Context, out io.Writer, dag *dagger.Client) error {
	var res struct {
		CurrentWorkspace struct {
			Modules []moduleListRow
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { modules { name source } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return err
	}
	return printModuleList(out, res.CurrentWorkspace.Modules)
}

func printModuleList(out io.Writer, modules []moduleListRow) error {
	if len(modules) == 0 {
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSOURCE"); err != nil {
		return err
	}
	for _, module := range modules {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", module.Name, module.Source); err != nil {
			return err
		}
	}
	return w.Flush()
}
