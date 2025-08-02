package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
)

var (
	generator      string
	listJSONOutput bool
)

func init() {
	clientListCmd.Flags().BoolVar(&listJSONOutput, "json", false, "Output the list of available clients in JSON format")

	clientCmd.AddCommand(clientInstallCmd)
	clientCmd.AddCommand(clientListCmd)
	clientCmd.AddCommand(clientUninstallCmd)
}

var clientCmd = &cobra.Command{
	Use:    "client",
	Short:  "Access Dagger client subcommands",
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

var clientInstallCmd = &cobra.Command{
	Use:     "install [options] generator [path]",
	Aliases: []string{"use"},
	Short:   "Generate a new Dagger client from the Dagger module",
	Example: "dagger client install go ./dagger",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			// default the output to the current working directory if it doesn't exist yet
			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}

			switch len(args) {
			case 0:
				return fmt.Errorf("generator must set (ts, go, python or custom generator)")
			case 1:
				generator = args[0]
				outputPath = filepath.Join(cwd, "dagger")
			case 2:
				generator = args[0]
				outputPath = args[1]
			}

			if filepath.IsAbs(outputPath) {
				outputPath, err = filepath.Rel(cwd, outputPath)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
			}

			dag := engineClient.Dagger()

			mod, err := initializeClientGeneratorModule(ctx, dag, ".")
			if err != nil {
				return fmt.Errorf("failed to initialize client generator module: %w", err)
			}

			contextDirPath, err := mod.Source.LocalContextDirectoryPath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get local context directory path: %w", err)
			}

			_, err = mod.Source.
				WithClient(generator, outputPath).
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to export client: %w", err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Generated client at %s\n", outputPath)

			return nil
		})
	},
	Annotations: map[string]string{
		"experimental": "true",
	},
}

var clientUninstallCmd = &cobra.Command{
	Use:     "uninstall [path]",
	Short:   "Remove a Dagger client from the module",
	Example: "dagger client uninstall ./dagger",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			if len(args) != 1 {
				return fmt.Errorf("expected only the path to the generated client to be set as argument")
			}

			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}

			path := args[0]

			if filepath.IsAbs(path) {
				path, err = filepath.Rel(cwd, path)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
			}

			dag := engineClient.Dagger()

			mod, err := initializeClientGeneratorModule(ctx, dag, ".")
			if err != nil {
				return fmt.Errorf("failed to initialize client generator module: %w", err)
			}

			contextDirPath, err := mod.Source.LocalContextDirectoryPath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get local context directory path: %w", err)
			}

			_, err = mod.Source.
				WithoutClient(path).
				GeneratedContextDirectory().
				Export(ctx, contextDirPath)
			if err != nil {
				return fmt.Errorf("failed to remove client from module: %w", err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w,
				"Client at %s removed from config.\n"+
					"Please manually remove any remaining files of the generated client from your host\n",
				path)

			return nil
		})
	},
}

//go:embed clientconf.graphql
var loadModClientConfQuery string

var clientListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all Dagger clients installed in the current module",
	Example: "dagger client list",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()

			mod, err := initializeClientGeneratorModule(ctx, dag, ".")
			if err != nil {
				return fmt.Errorf("failed to initialize client generator module: %w", err)
			}

			moduleSourceID, err := mod.Source.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to list clients: failed to get module source id: %w", err)
			}

			var res struct {
				Source struct {
					ConfigClients []struct {
						Generator string
						Directory string
					}
				}
			}

			err = dag.Do(ctx, &dagger.Request{
				Query: loadModClientConfQuery,
				Variables: map[string]any{
					"source": moduleSourceID,
				},
			}, &dagger.Response{
				Data: &res,
			})

			if err != nil {
				return fmt.Errorf("failed to list clients: failed to get module source config clients: %w", err)
			}

			if listJSONOutput {
				clientsContent, err := json.Marshal(res.Source.ConfigClients)
				if err != nil {
					return fmt.Errorf("failed to marshal clients results: %w", err)
				}

				cmd.OutOrStdout().Write(clientsContent)
				return nil
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
			fmt.Fprintf(tw, "GENERATOR\tPATH\n")
			for _, client := range res.Source.ConfigClients {
				fmt.Fprintf(tw, "%s\t%s\n", client.Generator, client.Directory)
			}

			return tw.Flush()
		})
	},
}
