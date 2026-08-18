package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
)

var (
	apiClientListJSON       bool
	apiClientInitNoGenerate bool
)

var apiClientCmd = &cobra.Command{
	Use:   "client",
	Short: "Manage generated API clients",
	Long: `Manage generated API clients for workspace modules.

Generated clients are persistent typed bindings to the API surface exposed by
one selected module. Client state is recorded in dagger.toml under the SDK
module that generates it.`,
}

var apiClientInitCmd = &cobra.Command{
	Use:   "init <sdk> <path> <module>",
	Short: "Initialize a generated API client",
	Long: `Initialize a generated API client at <path>.

<path> is relative to the current directory; a leading "/" means the workspace
root.

<sdk> is an SDK installed in this workspace. Run ` + "`dagger sdk install <sdk>`" + `
to add more choices.

The engine resolves <sdk> from dagger.toml, validates that it is installed as an
SDK, plans the generated files and workspace config change, then returns a
Changeset that the CLI previews and applies through the standard preview/apply
flow.

The SDK's generators run scoped to <path>, so the client's bindings come with
it. Pass --no-generate to record and scaffold the client without them.`,
	Example: "dagger api client init typescript ./lib/cli .dagger/modules/api",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var apiClientListCmd = &cobra.Command{
	Use:   "list",
	Short: "List generated API clients",
	Args:  cobra.NoArgs,
	RunE:  runAPIClientList,
}

func init() {
	apiClientListCmd.Flags().BoolVar(&apiClientListJSON, "json", false, "Output the client list in JSON format")
	apiClientInitCmd.PersistentFlags().BoolVar(&apiClientInitNoGenerate, "no-generate", false, "Skip running the SDK's generators for the new client")

	apiClientCmd.AddCommand(apiClientInitCmd, apiClientListCmd)
}

func runAPIClientInitWithSDK(cmd *cobra.Command, sdkName, clientPath, moduleRef string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("client init does not support --env; it scaffolds clients into the base workspace config")
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		sdkArgs, err := sdkInitArgsJSON(cmd)
		if err != nil {
			return err
		}
		opts := dagger.WorkspaceWithInitClientOpts{
			NoGenerate: apiClientInitNoGenerate,
		}
		if sdkArgs != "" {
			opts.Args = dagger.JSON(sdkArgs)
		}
		current := dag.CurrentWorkspace()
		// Root-measured for the same reason as module init: the workspace
		// config this edits can sit above the caller, and the apply happens
		// at the workspace root.
		updated := current.WithInitClient(clientPath, sdkName, moduleRef, opts).WithWorkdir(".")
		_, err = handleWorkspaceResponse(ctx, dag, current, updated, autoApply)
		return err
	})
}

type apiClientListEntry struct {
	SDK     string            `json:"sdk"`
	Path    string            `json:"path"`
	Module  string            `json:"module"`
	Pin     string            `json:"pin,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

func runAPIClientList(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, ec *client.Client) error {
		rawConfig, err := callWorkspaceConfigRead(ctx, ec.Dagger())
		if err != nil {
			return err
		}
		cfg, err := workspace.ParseConfig([]byte(rawConfig))
		if err != nil {
			return err
		}
		clients := apiClientEntries(cfg)
		if apiClientListJSON {
			out, err := json.Marshal(clients)
			if err != nil {
				return fmt.Errorf("marshal api clients: %w", err)
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		}

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
		fmt.Fprintf(tw, "SDK\tPATH\tMODULE\n")
		for _, client := range clients {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", client.SDK, client.Path, client.Module)
		}
		return tw.Flush()
	})
}

func callWorkspaceConfigRead(ctx context.Context, dag *dagger.Client) (string, error) {
	var res struct {
		CurrentWorkspace struct {
			ConfigRead string `json:"configRead"`
		} `json:"currentWorkspace"`
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query WorkspaceConfigRead {
  currentWorkspace {
    configRead(key: "")
  }
}`,
	}, &dagger.Response{Data: &res})
	if err != nil {
		return "", fmt.Errorf("read workspace config: %w", err)
	}
	return res.CurrentWorkspace.ConfigRead, nil
}

func apiClientEntries(cfg *workspace.Config) []apiClientListEntry {
	if cfg == nil {
		return nil
	}
	var entries []apiClientListEntry
	for sdkName, module := range cfg.Modules {
		if module.AsSDK == nil {
			continue
		}
		commandName := sdkCommandName(sdkName, module)
		for _, client := range module.AsSDK.Clients {
			entries = append(entries, apiClientListEntry{
				SDK:     commandName,
				Path:    client.Path,
				Module:  client.Module,
				Pin:     client.Pin,
				Options: client.Options,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SDK != entries[j].SDK {
			return entries[i].SDK < entries[j].SDK
		}
		return entries[i].Path < entries[j].Path
	})
	return entries
}
