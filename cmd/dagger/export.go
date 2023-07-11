package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:                   "export [artifact]",
	Aliases:               []string{"build"},
	DisableFlagsInUseLine: true,
	Long:                  `Export an artifact to the current directory.`,
	RunE:                  Export,
	SilenceUsage:          true,
}

func init() {
	rootCmd.AddCommand(exportCmd)

	// don't require -- to disambiguate subcommand flags
	exportCmd.Flags().SetInterspersed(false)

	exportCmd.Flags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")
}

func Export(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	focus = doFocus

	return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
		if err != nil {
			return fmt.Errorf("connect to dagger: %w", err)
		}

		if len(ProgrockEnv.Artifacts) == 0 {
			return fmt.Errorf("no checks defined")
		}

		var artifact DemoArtifact
		if len(args) > 0 {
			for _, c := range ProgrockEnv.Artifacts {
				if c.Name == args[0] {
					artifact = c
					break
				}
			}

			if artifact.Func == nil {
				return fmt.Errorf("artifact not found: %s", args[0])
			}
		} else {
			// TODO: default to the first one, or have an explicit default?
			artifact = ProgrockEnv.Artifacts[0]
		}

		dir, err := artifact.Func(Context{
			Context: ctx,
			client:  c,
		})
		if err != nil {
			return err
		}

		_, err = dir.Export(ctx, ".")
		return err
	})
}
