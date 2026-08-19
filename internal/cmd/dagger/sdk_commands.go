package daggercmd

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

func newSDKInfoCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show SDK information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSDKInfo(cmd, sdkName)
		},
	}
}

func newSDKModuleClaimCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "claim [path]",
		Short: "Claim an existing module source",
		Long:  "Claim an existing module so this SDK manages and regenerates it. The path defaults to the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKModuleClaim(cmd, sdkName, optionalPathArg(args))
		},
	}
}

func newSDKModuleUnclaimCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "unclaim [path]",
		Short: "Stop managing an existing module",
		Long:  "Stop this SDK from managing and regenerating an existing module. Files are left untouched. The path defaults to the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKModuleUnclaim(cmd, sdkName, optionalPathArg(args))
		},
	}
}

func newSDKModuleListCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List modules managed by this SDK",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSDKModuleList(cmd, sdkName)
		},
	}
}

func newSDKClientClaimCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "claim <path> <module>",
		Short: "Claim an existing generated client",
		Long:  "Claim an existing generated client so this SDK manages and regenerates it.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKClientClaim(cmd, sdkName, args[0], args[1])
		},
	}
}

func newSDKClientUnclaimCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "unclaim [path]",
		Short: "Stop managing a generated client",
		Long:  "Stop this SDK from managing and regenerating a client. Files are left untouched. The path defaults to the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKClientUnclaim(cmd, sdkName, optionalPathArg(args))
		},
	}
}

func newSDKClientListCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List clients managed by this SDK",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSDKClientList(cmd, sdkName)
		},
	}
}

func optionalPathArg(args []string) string {
	if len(args) == 0 {
		return "."
	}
	return args[0]
}

func runSDKInfo(cmd *cobra.Command, sdkName string) error {
	sdk, err := localSDK(sdkName)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "sdk-name: %s\nmodule-name: %s\nmodule-source: %s\nclaimed-modules: %d\n",
		sdk.commandName,
		sdk.moduleName,
		sdkModuleEntrySource(sdk.entry),
		len(sdk.entry.AsSDK.Modules),
	)
	return err
}

func runSDKModuleClaim(cmd *cobra.Command, sdkName, path string) error {
	return mutateSDKWorkspace(cmd, "module claim", func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithClaimedModule(sdkName, path)
	})
}

func runSDKModuleUnclaim(cmd *cobra.Command, sdkName, path string) error {
	return mutateSDKWorkspace(cmd, "module unclaim", func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithoutClaimedModule(sdkName, path)
	})
}

func runSDKClientClaim(cmd *cobra.Command, sdkName, path, module string) error {
	return mutateSDKWorkspace(cmd, "client claim", func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithClaimedClient(sdkName, path, module)
	})
}

func runSDKClientUnclaim(cmd *cobra.Command, sdkName, path string) error {
	return mutateSDKWorkspace(cmd, "client unclaim", func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithoutClaimedClient(sdkName, path)
	})
}

func mutateSDKWorkspace(cmd *cobra.Command, action string, mutate func(*dagger.Workspace) *dagger.Workspace) error {
	if workspaceEnv != "" {
		return fmt.Errorf("%s does not support --env; SDK claims live in the base workspace config", action)
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		current := dag.CurrentWorkspace()
		updated := mutate(current).WithWorkdir(".")
		_, err := handleWorkspaceResponse(ctx, dag, current, updated, autoApply)
		return err
	})
}

func runSDKModuleList(cmd *cobra.Command, sdkName string) error {
	sdk, err := localSDK(sdkName)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(sdk.entry.AsSDK.Modules))
	for _, module := range sdk.entry.AsSDK.Modules {
		paths = append(paths, module.Path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), path); err != nil {
			return err
		}
	}
	return nil
}

func runSDKClientList(cmd *cobra.Command, sdkName string) error {
	sdk, err := localSDK(sdkName)
	if err != nil {
		return err
	}
	clients := slices.Clone(sdk.entry.AsSDK.Clients)
	if len(clients) == 0 {
		return nil
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Path < clients[j].Path })
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "PATH\tMODULE\tPIN"); err != nil {
		return err
	}
	for _, claimed := range clients {
		var err error
		if claimed.Pin == "" {
			_, err = fmt.Fprintf(w, "%s\t%s\n", claimed.Path, claimed.Module)
		} else {
			_, err = fmt.Fprintf(w, "%s\t%s\t%s\n", claimed.Path, claimed.Module, claimed.Pin)
		}
		if err != nil {
			return err
		}
	}
	return w.Flush()
}

func localSDK(sdkName string) (configuredSDK, error) {
	cfg, _, err := readLocalWorkspaceConfig()
	if err != nil {
		return configuredSDK{}, err
	}
	return resolveConfiguredSDK(cfg, sdkName)
}
