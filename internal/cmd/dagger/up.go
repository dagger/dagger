package daggercmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var upListMode bool
var upDetachMode bool

//go:embed up.graphql
var loadUpQuery string

func init() {
	upCmd.Flags().BoolVarP(&upListMode, "list", "l", false, "List available services")
	upCmd.Flags().BoolVar(&upDetachMode, "detach", false, "Keep services running and publish ports in the background")
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
		if upDetachMode && upListMode {
			return exposeCommandUsageError(cmd, errors.New("--detach cannot be combined with --list"))
		}
		if upDetachMode && !exposePlatformSupported() {
			return errors.New("detached up and sessions expose are not yet supported on Windows")
		}
		return withEngine(
			cmd.Context(),
			client.Params{
				LoadWorkspaceModules: true,
				Detachable:           upDetachMode,
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
				if upDetachMode {
					return runDetachedServices(ctx, engineClient, services, cmd)
				}
				return runServices(ctx, services, cmd)
			},
		)
	},
}

func runDetachedServices(
	ctx context.Context,
	engineClient *client.Client,
	upGroup *dagger.UpGroup,
	cmd *cobra.Command,
) (rerr error) {
	transaction := &detachedUpTransaction{
		acknowledged: engineClient.DetachedQueryAcknowledged,
		terminate:    engineClient.Terminate,
	}
	defer func() {
		if rerr != nil && ctx.Err() != nil {
			rerr = idtui.ExitError{
				OriginalCode: 130,
				Original:     errors.Join(context.Cause(ctx), rerr),
			}
		}
	}()
	defer func() {
		rerr = errors.Join(rerr, transaction.rollback())
	}()

	services, err := upGroup.List(ctx)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return errors.New("no services matched")
	}
	groupID, err := upGroup.ID(ctx)
	if err != nil {
		return err
	}
	request, err := buildDetachedUpQueryRequest(groupID)
	if err != nil {
		return err
	}
	response, err := engineClient.SubmitPrimaryQuery(ctx, request, engine.QueryPresentation{
		Kind: "up", ResponsePath: []string{"node", "_startDetached"},
	})
	if err != nil {
		return err
	}
	if err := waitAtDetachedQueryAcknowledgedTestBarrier(response.SessionID, response.QueryID); err != nil {
		return err
	}
	query, err := waitForPrimaryQuery(ctx, engineClient.InspectPrimaryQuery)
	if err != nil {
		return err
	}
	result, err := engineClient.PrimaryQueryResult(ctx)
	if err != nil {
		return err
	}
	value, err := decodeDetachedResult(result.Body, query.Presentation)
	if err != nil {
		return err
	}
	upResult, ok := value.(core.DetachedUpResult)
	if !ok {
		return fmt.Errorf("saved up result is %T", value)
	}
	if err := engineClient.CloseAttachment(ctx); err != nil {
		return fmt.Errorf("close creator attachment: %w", err)
	}
	requestPorts, err := normalizeExposeRequest(upResult, nil)
	if err != nil {
		return err
	}
	stateDir, err := exposeStateDirectory()
	if err != nil {
		return err
	}
	preparation, err := prepareExposePorts(ctx, engineClient.SessionID, stateDir, requestPorts, true, false, "", func(message string) {
		fmt.Fprintln(cmd.ErrOrStderr(), message)
	})
	if err != nil {
		return err
	}
	if preparation.Startup != nil {
		transaction.abort = preparation.Startup.Abort
		if err := preparation.Startup.Commit(); err != nil {
			return err
		}
		transaction.committed = true
		return writeDetachedUpSummary(
			cmd.OutOrStdout(), engineClient.SessionID, preparation.Startup.Ports, preparation.Paths.Log,
		)
	}

	descriptor, err := engineClient.InspectSession(ctx)
	if err != nil {
		return err
	}
	transaction.committed = true
	return writeDetachedUpSummary(
		cmd.OutOrStdout(), engineClient.SessionID,
		exposedPortsFromDescriptor(descriptor, preparation.Request), preparation.Paths.Log,
	)
}

type detachedUpTransaction struct {
	acknowledged func() bool
	abort        func() error
	terminate    func(context.Context) error
	committed    bool
}

func (transaction *detachedUpTransaction) rollback() error {
	if transaction.committed {
		return nil
	}
	var rerr error
	if transaction.abort != nil {
		if err := transaction.abort(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("abort expose server: %w", err))
		}
	}
	if transaction.acknowledged == nil || !transaction.acknowledged() || transaction.terminate == nil {
		return rerr
	}
	terminateCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	if err := transaction.terminate(terminateCtx); err != nil {
		rerr = errors.Join(rerr, fmt.Errorf("rollback detached up session: %w", err))
	}
	return rerr
}

func buildDetachedUpQueryRequest(groupID dagger.ID) (json.RawMessage, error) {
	request, err := json.Marshal(&graphql.Request{
		Query: `query DetachedUp($id: ID!) {
  node(id: $id) {
    ... on UpGroup { _startDetached }
  }
}`,
		OpName:    "DetachedUp",
		Variables: map[string]any{"id": groupID},
	})
	if err != nil {
		return nil, fmt.Errorf("encode detached up query: %w", err)
	}
	return request, nil
}

func writeDetachedUpSummary(
	w io.Writer,
	sessionID string,
	ports []exposedPort,
	logPath string,
) error {
	if _, err := fmt.Fprintf(w, "\nDetached session %s\n", sessionID); err != nil {
		return err
	}
	if formatted := formatExposedPorts(ports); formatted != "" {
		if _, err := fmt.Fprintln(w, formatted); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Ports served by a background process on this machine (log: %s)\n\n", logPath); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Inspect:    dagger sessions inspect %s\n", sessionID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Re-expose:  dagger sessions expose %s\n", sessionID); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Terminate:  dagger sessions terminate %s\n", sessionID)
	return err
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
