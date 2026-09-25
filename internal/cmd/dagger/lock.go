package daggercmd

import (
	"context"
	"fmt"
	"io"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var lockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Manage Dagger lockfiles",
	Args:  cobra.NoArgs,
}

func init() {
	lockCmd.AddCommand(newLockListCmd())
	lockCmd.AddCommand(newLockUpdateCmd())
}

func newLockListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [SELECTOR...]",
		Short: "List matching lockfile entries",
		Long: `List entries already recorded in the selected dagger.lock.

Selectors use the same matching rules as dagger lock update. With no selectors,
every supported entry is listed. The command prints canonical resource
identities without resolving or writing them.`,
		Example: `  dagger lock list
  dagger lock list 'node:lts*' 'dagger@m*'
  dagger lock list 'github.com/dagger/dagger@refs/heads/main'`,
		Args: cobra.ArbitraryArgs,
		RunE: runLockList,
	}
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	setWorkspaceFlagPolicy(cmd)
	return cmd
}

func newLockUpdateCmd() *cobra.Command {
	var noGenerate bool
	var list bool
	cmd := &cobra.Command{
		Use:   "update [SELECTOR...]",
		Short: "Refresh selected lockfile entries",
		Long: `Refresh entries already recorded in the selected dagger.lock.

Selectors match user-facing resource identities instead of lockfile entry
types. OCI entries match their full image reference, repository name, or short
reference with a tag. For example, both "node" and "node:lts*" match
"docker.io/library/node:lts-alpine". Git entries match a scheme-free repository
identity or repository name, optionally followed by a ref. "dagger@main" and
"dagger@m*" match both "refs/heads/main" and "refs/tags/main" in repositories
named dagger. Use a qualified ref such as
"github.com/dagger/dagger@refs/heads/main" to select only that branch. Vanity
URL entries match their full URL or final path component.

Selectors support shell-style patterns, with "*" matching across "/". Multiple
selectors are ORed together, but every selector must match at least one entry.
With no selectors, every supported entry is refreshed.

SDK client scopes are regenerated unless --no-generate is set. Use --list to
print the matching canonical identities without resolving or writing them. This
is equivalent to dagger lock list with the same selectors.`,
		Example: `  dagger lock update
  dagger lock update 'node:lts*' 'dagger@m*'
  dagger lock update 'github.com/dagger/dagger@refs/heads/main'
  dagger lock update '*node*' '*python*'
  dagger lock update --list dagger`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, selectors []string) error {
			if list && noGenerate {
				return fmt.Errorf("--list and --no-generate cannot be used together")
			}
			if list {
				return runLockList(cmd, selectors)
			}
			return runLockUpdate(cmd, selectors, noGenerate)
		},
	}
	cmd.Flags().BoolVar(&noGenerate, "no-generate", false, "Update the lockfile without regenerating SDK client scopes")
	cmd.Flags().BoolVarP(&list, "list", "l", false, "List matching lock entries without resolving or updating them")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	setWorkspaceFlagPolicy(cmd)
	return cmd
}

func runModuleUpdate(cmd *cobra.Command, names []string) error {
	version, err := cmd.Flags().GetString("version")
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("version") && version == "" {
		return fmt.Errorf("--version must not be empty")
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		modules, cwd, err := installedModulesForSelection(ctx, dag)
		if err != nil {
			return err
		}
		selections, err := workspace.SelectModuleUpdates(modules, ".", cwd, names, version)
		if err != nil {
			return err
		}
		for _, selection := range selections {
			if err := writeModuleSourceMatch(cmd.ErrOrStderr(), selection); err != nil {
				return err
			}
			if selection.Version != "" {
				_, previous, hasVersion, _ := workspace.SplitModuleVersion(selection.Entry.Source)
				if !hasVersion {
					previous = "(default)"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Updating %q: %s -> %s.\n", selection.Name, previous, selection.Version); err != nil {
					return err
				}
			}
		}
		var result struct {
			CurrentWorkspace struct {
				Result struct {
					ID dagger.ID
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query: `query ModuleUpdate($names: [String!]!, $version: String) {
  currentWorkspace {
    result: withUpdatedModules(names: $names, version: $version) { id }
  }
}`,
			Variables: map[string]any{"names": names, "version": version},
		}, &dagger.Response{Data: &result}); err != nil {
			return err
		}
		if result.CurrentWorkspace.Result.ID == "" {
			return fmt.Errorf("module update returned no workspace")
		}
		current := dag.CurrentWorkspace().WithWorkdir(".")
		updated := dagger.Ref[*dagger.Workspace](dag, result.CurrentWorkspace.Result.ID).WithWorkdir(".")
		return updateMaterializedWorkspace(ctx, cmd.OutOrStdout(), dag, current, updated)
	})
}

func runLockList(cmd *cobra.Command, selectors []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return printSelectedLockEntries(ctx, cmd.OutOrStdout(), engineClient.Dagger(), selectors)
	})
}

func runLockUpdate(cmd *cobra.Command, selectors []string, noGenerate bool) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateWorkspaceLockfile(ctx, cmd.OutOrStdout(), engineClient.Dagger(), selectors, noGenerate)
	})
}

func printSelectedLockEntries(ctx context.Context, outWriter io.Writer, dag *dagger.Client, selectors []string) error {
	var result struct {
		CurrentWorkspace struct {
			Entries []string `json:"__lockEntries"`
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query LockUpdateDryRun($selectors: [String!]!) {
  currentWorkspace {
    __lockEntries(selectors: $selectors)
  }
}`,
		Variables: map[string]any{"selectors": selectors},
	}, &dagger.Response{Data: &result}); err != nil {
		return err
	}
	if len(result.CurrentWorkspace.Entries) == 0 {
		_, err := fmt.Fprintln(outWriter, "No lock entries")
		return err
	}
	for _, entry := range result.CurrentWorkspace.Entries {
		if _, err := fmt.Fprintln(outWriter, entry); err != nil {
			return err
		}
	}
	return nil
}

func updateWorkspaceLockfile(ctx context.Context, outWriter io.Writer, dag *dagger.Client, selectors []string, noGenerate bool) error {
	current := dag.CurrentWorkspace()
	var result struct {
		CurrentWorkspace struct {
			Updated struct {
				ID dagger.ID
			} `json:"updated"`
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query LockUpdate($selectors: [String!]!, $noGenerate: Boolean!) {
  currentWorkspace {
    updated: __withUpdatedLock(selectors: $selectors, noGenerate: $noGenerate) { id }
  }
}`,
		Variables: map[string]any{"selectors": selectors, "noGenerate": noGenerate},
	}, &dagger.Response{Data: &result}); err != nil {
		return err
	}
	if result.CurrentWorkspace.Updated.ID == "" {
		return fmt.Errorf("lock update returned no workspace")
	}
	updated := dagger.Ref[*dagger.Workspace](dag, result.CurrentWorkspace.Updated.ID)
	return updateMaterializedWorkspace(ctx, outWriter, dag, current, updated)
}

func updateMaterializedWorkspace(ctx context.Context, outWriter io.Writer, dag *dagger.Client, current, updated *dagger.Workspace) error {
	updated, err := materializeWorkspace(ctx, dag, updated)
	if err != nil {
		return err
	}
	isEmpty, err := updated.Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !isEmpty {
		if err := updated.Export(ctx); err != nil {
			return err
		}
	}
	return writeWorkspaceUpdateResult(outWriter, isEmpty)
}

func writeWorkspaceUpdateResult(outWriter io.Writer, isEmpty bool) error {
	if isEmpty {
		_, err := outWriter.Write([]byte("Lockfile already up to date\n"))
		return err
	}

	_, err := outWriter.Write([]byte("Updated dagger.lock\n"))
	return err
}
