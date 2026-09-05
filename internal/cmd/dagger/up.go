package daggercmd

import (
	"context"
	_ "embed"

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
	Use:   "up [options] [pattern...]",
	Short: "Run your project's services for local development — databases, APIs, dev servers, etc.",
	Long: `Run your project's services for local development — databases, APIs, dev servers, etc.

Examples:
  dagger up                       # Start all services
  dagger up -l                    # List all available services
  dagger up web                   # Start only the 'web' service
`,
	Args: cobra.ArbitraryArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(
			cmd.Context(),
			client.Params{
				LoadWorkspaceModules: true,
			},
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
	items := make([]commandListItem, 0, len(info.Ups))
	for _, up := range info.Ups {
		items = append(items, commandListItem{
			Name:    up.Name,
			Comment: firstDescriptionLine(up.Description),
		})
	}
	return writeCommandList(cmd.OutOrStdout(), items)
}

func runServices(ctx context.Context, upGroup *dagger.UpGroup, _ *cobra.Command) error {
	ctx, zoomSpan := Tracer().Start(ctx, "services", telemetry.Passthrough())
	defer zoomSpan.End()
	zoomID := dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()}
	Frontend.SetPrimary(zoomID)
	// This run is ABOUT the services it starts: install the command screen
	// that leads with each service's display span (its rolled-up health-check
	// and service logs, and its ready-URL chip) instead of the setup tree.
	installUpView(Frontend, zoomID)
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// Run blocks until context cancellation (Ctrl+C). Treat that as a clean
	// shutdown rather than surfacing a cancellation error to the user.
	_, err := upGroup.Run().ID(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
