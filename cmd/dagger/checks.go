package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"

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
			if checksListMode {
				return listChecks(ctx, checks, cmd)
			}
			return runChecks(ctx, checks, cmd)
		})
	},
}

func withInternalSpan(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	ctx, span := Tracer().Start(ctx, name)
	err := fn(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
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

func loadCheckGroupInfo(ctx context.Context, checks []dagger.Check) (*CheckGroupInfo, error) {
	info := &CheckGroupInfo{}
	err := withInternalSpan(ctx, "fetch check information", func(ctx context.Context) error {
		for _, check := range checks {
			checkInfo := &CheckInfo{}

			name, err := check.Name(ctx)
			if err != nil {
				return err
			}
			checkInfo.Name = cliName(name)

			description, err := check.Description(ctx)
			if err != nil {
				return err
			}
			checkInfo.Description = description

			emoji, err := check.ResultEmoji(ctx)
			if err != nil {
				return err
			}
			checkInfo.Emoji = emoji

			message, err := check.Message(ctx)
			if err != nil {
				return err
			}
			checkInfo.Message = message

			info.Checks = append(info.Checks, checkInfo)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

type CheckGroupInfo struct {
	Checks []*CheckInfo
}

type CheckInfo struct {
	Name        string
	Description string
	Emoji       string
	Message     string
}

// 'dagger checks -l'
func listChecks(ctx context.Context, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	checks, err := checkgroup.List(ctx)
	if err != nil {
		return err
	}
	info, err := loadCheckGroupInfo(ctx, checks)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, check := range info.Checks {
		firstLine := check.Description
		if idx := strings.Index(check.Description, "\n"); idx != -1 {
			firstLine = check.Description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", check.Name, firstLine)
	}
	return tw.Flush()
}

// 'dagger checks' (runs by default)
func runChecks(ctx context.Context, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	checks, err := checkgroup.Run().List(ctx)
	if err != nil {
		return err
	}
	info, err := loadCheckGroupInfo(ctx, checks)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Result").Bold(),
		termenv.String("Message").Bold(),
	)
	for _, check := range info.Checks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", check.Name, check.Emoji, check.Message)
	}
	return tw.Flush()
}

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List checks without running them")
	checksCmd.Flags().StringSliceVar(&checksExclude, "exclude", nil, "Exclude checks matching the specified patterns")
}
