package daggercmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

func installedModulesForSelection(ctx context.Context, dag *dagger.Client) (map[string]workspace.ModuleEntry, string, error) {
	var result struct {
		CurrentWorkspace struct {
			Cwd     string
			Modules []struct{ Name, Source string }
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query InstalledModuleSources { currentWorkspace { cwd modules { name source } } }`,
	}, &dagger.Response{Data: &result}); err != nil {
		return nil, "", err
	}
	modules := make(map[string]workspace.ModuleEntry, len(result.CurrentWorkspace.Modules))
	for _, module := range result.CurrentWorkspace.Modules {
		modules[module.Name] = workspace.ModuleEntry{Source: module.Source}
	}
	return modules, strings.TrimPrefix(result.CurrentWorkspace.Cwd, "/"), nil
}

func writeModuleSourceMatch(out io.Writer, selection workspace.ModuleSelection) error {
	if selection.Source == "" {
		return nil
	}
	_, err := fmt.Fprintf(out, "Matched installed module %q by source %q.\n", selection.Name, selection.Source)
	return err
}

var moduleVersionCmd = &cobra.Command{
	Use:   "version NAME|SOURCE",
	Short: "Print the version request for an installed module",
	Long: `Print the version request from dagger.toml, such as v1 or main.

Match an installed name first, then a source without a version. The source must
match exactly one installation. Source-match details go to stderr.
Local modules and sources without an explicit version request return an error.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, ec *client.Client) error {
			modules, cwd, err := installedModulesForSelection(ctx, ec.Dagger())
			if err != nil {
				return err
			}
			selection, err := workspace.SelectModule(modules, ".", cwd, args[0], false)
			if err != nil {
				return err
			}
			version, err := installedModuleVersion(selection)
			if err != nil {
				return err
			}
			if err := writeModuleSourceMatch(cmd.ErrOrStderr(), selection); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		})
	},
}

func installedModuleVersion(selection workspace.ModuleSelection) (string, error) {
	if workspace.IsLocalRef(selection.Entry.Source, "") {
		return "", fmt.Errorf("module %q has a local source; no version is targeted", selection.Name)
	}
	_, version, hasVersion, err := workspace.SplitModuleVersion(selection.Entry.Source)
	if err != nil {
		return "", err
	}
	if !hasVersion {
		return "", fmt.Errorf("module %q has no explicit version request", selection.Name)
	}
	return version, nil
}
