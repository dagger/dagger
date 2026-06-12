package main

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
	apiClientListJSON bool
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

<sdk> is an SDK installed in this workspace. Run ` + "`dagger sdk install <sdk>`" + `
to add more choices.

The engine validates that <sdk> is installed as an SDK in dagger.toml, plans the
generated files and workspace config change, then returns a Changeset that the
CLI previews and applies through the standard preview/apply flow.`,
	Example: "dagger api client init typescript ./lib/cli .dagger/modules/api",
	Args:    cobra.ExactArgs(3),
	RunE:    runAPIClientInit,
}

var apiClientListCmd = &cobra.Command{
	Use:   "list",
	Short: "List generated API clients",
	Args:  cobra.NoArgs,
	RunE:  runAPIClientList,
}

func init() {
	apiClientListCmd.Flags().BoolVar(&apiClientListJSON, "json", false, "Output the client list in JSON format")

	apiClientCmd.AddCommand(apiClientInitCmd, apiClientListCmd)
}

func runAPIClientInit(cmd *cobra.Command, args []string) error {
	sdkName := args[0]
	clientPath := args[1]
	moduleRef := args[2]

	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()

		exportPath, err := currentWorkspaceExportPath(ctx, dag.CurrentWorkspace())
		if err != nil {
			return err
		}

		changesetID, err := callClientInit(ctx, dag, clientPath, sdkName, moduleRef)
		if err != nil {
			return err
		}

		return handleChangesetResponseAt(ctx, dag, changesetID, autoApply, exportPath)
	})
}

func callClientInit(
	ctx context.Context,
	dag *dagger.Client,
	path string,
	sdkName string,
	moduleRef string,
) (string, error) {
	var res struct {
		CurrentWorkspace struct {
			ClientInit struct {
				ID string `json:"id"`
			} `json:"clientInit"`
		} `json:"currentWorkspace"`
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query ClientInit($path: String!, $sdk: String!, $module: String!) {
  currentWorkspace {
    clientInit(path: $path, sdk: $sdk, module: $module) {
      id
    }
  }
}`,
		Variables: map[string]any{
			"path":   path,
			"sdk":    sdkName,
			"module": moduleRef,
		},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return "", fmt.Errorf("plan api client init: %w", err)
	}
	if res.CurrentWorkspace.ClientInit.ID == "" {
		return "", fmt.Errorf("api client init returned an empty changeset id")
	}
	return res.CurrentWorkspace.ClientInit.ID, nil
}

func callClientGenerate(ctx context.Context, dag *dagger.Client) (string, error) {
	var res struct {
		CurrentWorkspace struct {
			ClientGenerate struct {
				ID string `json:"id"`
			} `json:"clientGenerate"`
		} `json:"currentWorkspace"`
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query ClientGenerate {
  currentWorkspace {
    clientGenerate {
      id
    }
  }
}`,
	}, &dagger.Response{Data: &res})
	if err != nil {
		return "", fmt.Errorf("generate api clients: %w", err)
	}
	if res.CurrentWorkspace.ClientGenerate.ID == "" {
		return "", fmt.Errorf("api client generation returned an empty changeset id")
	}
	return res.CurrentWorkspace.ClientGenerate.ID, nil
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
		for _, client := range module.AsSDK.Clients {
			entries = append(entries, apiClientListEntry{
				SDK:     sdkName,
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
