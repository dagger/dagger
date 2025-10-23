package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
)

var (
	checksListMode bool
	checksExclude  []string
)

var checksCmd = &cobra.Command{
	Use:   "checks [options] [pattern...]",
	Short: "Load and execute checks",
	Long: `Load and execute checks using the Checks API.

Run all checks and print results, or list available checks.

Examples:
  dagger checks                    # Run all checks
  dagger checks -l                 # List all checks without running
  dagger checks pattern1 pattern2  # Run checks matching patterns
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			_, err := initializeDefaultModule(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}
			// Build include patterns from args
			var includePatterns []string
			if len(args) > 0 {
				includePatterns = args
			}
			// Get checks with include patterns
			var checksGroup *dagger.CheckGroup
			if len(includePatterns) > 0 {
				checksGroup = dag.Checks(dagger.ChecksOpts{Include: includePatterns})
			} else {
				checksGroup = dag.Checks()
			}
			if checksListMode {
				return listChecks(ctx, checksGroup, cmd)
			}
			return runChecks(ctx, checksGroup, cmd)
		})
	},
}

func listChecks(ctx context.Context, checksGroup *dagger.CheckGroup, cmd *cobra.Command) error {
	checks, err := checksGroup.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list checks: %w", err)
	}

	for _, check := range checks {
		fullName, err := check.FullName(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check full name: %w", err)
		}
		cliName := cliName(fullName)
		description, err := check.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check description: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", cliName, description)
	}

	return nil
}

func runChecks(ctx context.Context, checksGroup *dagger.CheckGroup, cmd *cobra.Command) error {
	// Run the checks
	runGroup := checksGroup.Run()

	// Get the results
	checks, err := runGroup.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to run checks: %w", err)
	}

	for _, check := range checks {
		fullName, err := check.FullName(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check full name: %w", err)
		}

		emoji, err := check.ResultEmoji(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check result emoji: %w", err)
		}

		message, err := check.Message(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check message: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", fullName, emoji, message)
	}

	return nil
}

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List checks without running them")
	checksCmd.Flags().StringSliceVar(&checksExclude, "exclude", nil, "Exclude checks matching the specified patterns")
}
