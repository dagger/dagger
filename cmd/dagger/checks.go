package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
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
			mod, err := loadModule(ctx, dag)
			if err != nil {
				return err
			}
			var checks *dagger.CheckGroup
			if len(args) > 0 {
				checks = mod.Checks(dagger.ModuleChecksOpts{Include: args})
			} else {
				checks = mod.Checks()
			}
			//Create an "internal" span to tuck away the noise under the default verbosity level
			internalCtx, internalSpan := Tracer().Start(ctx, "load checks", telemetry.Internal())
			defer internalSpan.End()
			if checksListMode {
				return listChecks(internalCtx, checks, cmd)
			}
			return runChecks(internalCtx, checks, cmd)
		})
	},
}

func loadModule(ctx context.Context, dag *dagger.Client) (*dagger.Module, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	ctx, span := Tracer().Start(ctx, "load "+modRef)
	defer span.End()
	return dag.ModuleSource(modRef).AsModule().Sync(ctx)
}

// 'dagger checks -l'
func listChecks(ctx context.Context, checksGroup *dagger.CheckGroup, cmd *cobra.Command) error {
	checks, err := checksGroup.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list checks: %w", err)
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, check := range checks {
		name, err := check.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check full name: %w", err)
		}
		cliName := cliName(name)
		description, err := check.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check description: %w", err)
		}
		// Extract only the first line of the description
		firstLine := description
		if idx := strings.Index(description, "\n"); idx != -1 {
			firstLine = description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n",
			cliName,
			firstLine,
		)
	}
	return tw.Flush()
}

// 'dagger checks' (runs by default)
func runChecks(ctx context.Context, checksGroup *dagger.CheckGroup, cmd *cobra.Command) error {
	// Get the results
	checks, err := checksGroup.Run().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to run checks: %w", err)
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Result").Bold(),
		termenv.String("Message").Bold(),
	)
	for _, check := range checks {
		name, err := check.Name(ctx)
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
		fmt.Fprintf(tw, "%s\t%s\\n",
			name,
			emoji,
			message,
		)
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", name, emoji, message)
	}

	return nil
}

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List checks without running them")
	checksCmd.Flags().StringSliceVar(&checksExclude, "exclude", nil, "Exclude checks matching the specified patterns")
}
