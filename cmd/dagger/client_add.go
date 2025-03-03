package main

import (
	"context"
	"fmt"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/spf13/cobra"
)

var (
	generator string
	dev  bool
)

func init() {
	clientAddCmd.Flags().StringVar(&generator, "generator", "", "Generator to use to generate the client")
	clientAddCmd.Flags().BoolVar(&dev, "dev", false, "Generate in developer mode")
}

var clientAddCmd = &cobra.Command{
	Use:     "add [options] [path]",
	Short:   "Generate a new Dagger client from the Dagger module",
	Example: "dagger client add --generator=go ./dagger",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			if generator == "" {
				return fmt.Errorf("generator must set (ts, go, python or custom generator)")
			}

			// default the output to the current working directory if it doesn't exist yet
			cwd, err := pathutil.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}

			outputPath := filepath.Join(cwd, "dagger")
			if len(args) > 0 {
				outputPath = args[0]
			}

			if filepath.IsAbs(outputPath) {
				outputPath, err = filepath.Rel(cwd, outputPath)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
			}

			handler := &clientAddHandler{
				dag:        engineClient.Dagger(),
				cmd:        cmd,
				outputPath: outputPath,
			}

			return handler.Run(ctx)
		})
	},
	Annotations: map[string]string{
		"experimental": "true",
	},
}

type clientAddHandler struct {
	dag        *dagger.Client
	cmd        *cobra.Command
	outputPath string
}

func (c *clientAddHandler) Run(ctx context.Context) (rerr error) {
	mod, _, err := initializeClientGeneratorModule(ctx, c.dag, ".")
	if err != nil {
		return fmt.Errorf("failed to initialize client generator module: %w", err)
	}

	contextDirPath, err := mod.Source.LocalContextDirectoryPath(ctx)
	if err != nil {
		return fmt.Errorf("failed to get local context directory path: %w", err)
	}

	_, err = mod.Source.
		WithClient(generator, c.outputPath, dagger.ModuleSourceWithClientOpts{
			Dev: dev,
		}).
		GeneratedContextDirectory().
		Export(ctx, contextDirPath)
	if err != nil {
		return fmt.Errorf("failed to export client: %w", err)
	}

	w := c.cmd.OutOrStdout()
	fmt.Fprintf(w, "Generated client at %s\n", c.outputPath)

	return nil
}
