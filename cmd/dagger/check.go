package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:                   "check [suite]",
	Aliases:               []string{"test"},
	DisableFlagsInUseLine: true,
	Long:                  `Run your environment's checks.`,
	RunE:                  Check,
	SilenceUsage:          true,
	SilenceErrors:         true,
}

func init() {
	rootCmd.AddCommand(checkCmd)

	// don't require -- to disambiguate subcommand flags
	checkCmd.Flags().SetInterspersed(false)

	checkCmd.Flags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")
}

func Check(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	focus = doFocus

	return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
		if err != nil {
			return fmt.Errorf("connect to dagger: %w", err)
		}

		if len(ProgrockEnv.Checks) == 0 {
			return fmt.Errorf("no checks defined")
		}

		var check DemoCheck
		if len(args) > 0 {
			for _, c := range ProgrockEnv.Checks {
				if c.Name == args[0] {
					check = c
					break
				}
			}
		} else {
			// TODO: default to the first one, or have an explicit default?
			check = ProgrockEnv.Checks[0]
		}

		_, err = check.Func(Context{
			Context: ctx,
			client:  c,
		})

		return err
	})
}
