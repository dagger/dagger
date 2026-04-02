package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var upListMode bool

//go:embed up.graphql
var loadUpQuery string

func init() {
	upCmd.Flags().BoolVarP(&upListMode, "list", "l", false, "List available services")
}

var upCmd = &cobra.Command{
	Hidden: true,
	Use:    "up [options] [pattern...]",
	Short:  "Start services defined by the module and expose them on the host",
	Long: `Start services defined by the module and expose them on the host

Examples:
  dagger up                       # Start all services
  dagger up -l                    # List all available services
  dagger up web                   # Start only the 'web' service
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(
			cmd.Context(),
			client.Params{},
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				ws := dag.CurrentWorkspace()
				var services *dagger.UpGroup
				if len(args) > 0 {
					services = ws.Services(dagger.WorkspaceServicesOpts{Include: args})
				} else {
					services = ws.Services()
				}
				if upListMode {
					return listServices(ctx, dag, services, cmd)
				}
				return runServices(ctx, services, cmd)
			},
		)
	},
}

func loadUpGroupInfo(ctx context.Context, dag *dagger.Client, upGroup *dagger.UpGroup) (*UpGroupInfo, error) {
	items, err := loadGroupListDetails(ctx, dag, "fetch service information",
		func(ctx context.Context) (any, error) { return upGroup.ID(ctx) },
		loadUpQuery, "UpGroupListDetails",
	)
	if err != nil {
		return nil, err
	}
	info := &UpGroupInfo{Ups: make([]*UpInfo, 0, len(items))}
	for _, item := range items {
		info.Ups = append(info.Ups, &UpInfo{
			Name:        cliName(item.Name),
			Description: item.Description,
		})
	}
	return info, nil
}

type UpGroupInfo struct {
	Ups []*UpInfo
}

type UpInfo struct {
	Name        string
	Description string
}

func listServices(ctx context.Context, dag *dagger.Client, upGroup *dagger.UpGroup, cmd *cobra.Command) error {
	info, err := loadUpGroupInfo(ctx, dag, upGroup)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, up := range info.Ups {
		firstLine := up.Description
		if idx := strings.Index(up.Description, "\n"); idx != -1 {
			firstLine = up.Description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", up.Name, firstLine)
	}
	return tw.Flush()
}

func runServices(ctx context.Context, upGroup *dagger.UpGroup, _ *cobra.Command) error {
	ctx, zoomSpan := Tracer().Start(ctx, "services", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// Run blocks until context cancellation (Ctrl+C). Treat that as a clean
	// shutdown rather than surfacing a cancellation error to the user.
	_, err := upGroup.Run().ID(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
