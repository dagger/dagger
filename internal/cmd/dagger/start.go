package daggercmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

var (
	startListMode bool
	// noForwardFlag is start's --no-forward.
	noForwardFlag bool
)

func init() {
	registerCommandArtifactFlags(startCmd)
	startCmd.Flags().BoolVarP(&startListMode, "list", "l", false, "List available services")
	startCmd.Flags().BoolVar(&detachFlag, "detach", false, "Run the services and forward their ports in the background, in a detached session (experimental)")
	startCmd.Flags().BoolVar(&noForwardFlag, "no-forward", false, "With --detach, only start the services, leaving no local process; forward them later with 'dagger --session ID start --detach' (experimental)")
}

var startCmd = &cobra.Command{
	Use:     "start [FILTERS] [OPTIONS]",
	Aliases: []string{"up"},
	Short:   "Run your project's services for local development — databases, APIs, dev servers, etc.",
	Args:    cobra.ArbitraryArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
		// "up" is the former name of this command.
		hiddenAliasesAnnotation: "up",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if noForwardFlag && !detachFlag && !inBackground {
			return fmt.Errorf("--no-forward requires --detach")
		}
		if detachFlag && !startListMode {
			return runDetached(cmd.OutOrStdout())
		}
		if !startListMode {
			previous := opts.RootFilter
			opts.RootFilter = (*dagui.DB).ServiceDisplaySpans
			defer func() { opts.RootFilter = previous }()
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
		if err != nil {
			return err
		}
		params = backgroundClientParams(params)
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				ws := core.NewQuery(dag).CurrentWorkspace()
				all, err := commandArtifactsWithFlags(ctx, dag, ws, cmd, args, true)
				if err != nil {
					return err
				}
				services, err := commandArtifactTargets(ctx, dag, cmd, all)
				if err != nil {
					return err
				}
				if startListMode {
					return listArtifactSelection(ctx, dag, services, cmd)
				}
				return runServices(ctx, dag, services, cmd)
			},
		)
	},
}

func runServices(ctx context.Context, dag *dagger.Client, services *core.Artifacts, _ *cobra.Command) (rerr error) {
	ctx, zoomSpan := Tracer().Start(ctx, "services", telemetry.Passthrough())
	// The report uses this span's failure to include the cause and its logs.
	defer telemetry.EndWithCause(zoomSpan, &rerr)
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// Services hold their slot until they stop, so a limit would leave queued
	// services waiting forever.
	results, err := evaluateArtifacts(ctx, dag, services, false, 0)
	if err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("no services found")
	}
	if noForwardFlag {
		return startServices(ctx, dag, results)
	}
	cfg, err := artifactWorkspaceConfig(ctx, core.NewQuery(dag).CurrentWorkspace())
	if err != nil {
		return err
	}
	mappings := make([][]core.PortForward, len(results))
	ports := map[string]string{}
	for i, result := range results {
		if result.Value == nil || result.Value.Type != "Service" {
			return fmt.Errorf("%s did not return a Service", result.Artifact.URI)
		}
		name := strings.Join(result.Artifact.Path, "/")
		for host, mapping := range cfg.Ports {
			backend := strings.ReplaceAll(mapping.BackendService, ":", "/")
			for moduleName, module := range cfg.Modules {
				if module.Main {
					backend = strings.TrimPrefix(backend, moduleName+"/")
				}
			}
			if backend != name {
				continue
			}
			frontend, err := strconv.Atoi(host)
			if err != nil {
				return err
			}
			mappings[i] = append(mappings[i], core.PortForward{Frontend: frontend, Backend: mapping.BackendPort, Protocol: core.NetworkProtocolTcp})
		}
		var claimed []string
		if len(mappings[i]) > 0 {
			for _, port := range mappings[i] {
				claimed = append(claimed, fmt.Sprintf("%d/tcp", port.Frontend))
			}
		} else {
			var response struct {
				Node struct {
					Ports []struct {
						Port     int
						Protocol string
					}
				}
			}
			err := dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) {
				node(id: $id) { ... on Service { ports(declared: true) { port protocol } } }
			}`, Variables: map[string]any{"id": result.Value.ID}}, &dagger.Response{Data: &response})
			if err != nil {
				return err
			}
			for _, port := range response.Node.Ports {
				claimed = append(claimed, fmt.Sprintf("%d/%s", port.Port, strings.ToLower(port.Protocol)))
			}
		}
		for _, port := range claimed {
			if owner, exists := ports[port]; exists {
				return fmt.Errorf("port collision detected: port %s is exposed by both %q and %q", port, owner, result.Artifact.URI)
			}
			ports[port] = result.Artifact.URI
		}
	}
	// A detached start has started once every port forward listens.
	var readyMu sync.Mutex
	var readyURLs []string
	var listening sync.WaitGroup
	listening.Add(len(results))
	go func() {
		listening.Wait()
		reportBackgroundStarted(readyURLs)
	}()
	jobs, runCtx := errgroup.WithContext(ctx)
	for i, result := range results {
		jobs.Go(func() (err error) {
			serviceCtx, span := Tracer().Start(runCtx, result.Artifact.URI, trace.WithAttributes(attribute.String(telemetryattrs.ServiceNameAttr, result.Artifact.URI), attribute.Bool(telemetry.UIRollUpLogsAttr, true)))
			defer telemetry.EndWithCause(span, &err)
			service := core.Ref[*core.Service](core.NewQuery(dag), result.Value.ID)
			// Start the service on its own, so that it belongs to the session
			// rather than to this client's port forward, and keeps running
			// when this client leaves.
			if _, err := service.Start(serviceCtx); err != nil {
				return err
			}
			tunnel, err := core.NewQuery(dag).Host().Tunnel(service, core.HostTunnelOpts{Ports: mappings[i], Native: len(mappings[i]) == 0}).Start(serviceCtx)
			if err != nil {
				return err
			}
			ports, err := tunnel.Ports(serviceCtx)
			if err != nil {
				return err
			}
			urls := make([]string, 0, len(ports))
			for _, port := range ports {
				number, err := port.Port(serviceCtx)
				if err != nil {
					return err
				}
				scheme := "http"
				if number == 443 {
					scheme = "https"
				}
				urls = append(urls, fmt.Sprintf("%s://localhost:%d", scheme, number))
			}
			span.SetAttributes(attribute.StringSlice(telemetryattrs.ServiceURLsAttr, urls))
			readyMu.Lock()
			readyURLs = append(readyURLs, urls...)
			readyMu.Unlock()
			listening.Done()
			_, ready := Tracer().Start(serviceCtx, "ready "+strings.Join(urls, " "), trace.WithAttributes(attribute.StringSlice(telemetryattrs.ServiceURLsAttr, urls)))
			defer ready.End()
			<-serviceCtx.Done()
			return nil
		})
	}
	err = jobs.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// startServices starts the services in the session and leaves them running,
// with no forwarding.
func startServices(ctx context.Context, dag *dagger.Client, results []artifactValueResult) error {
	jobs, ctx := errgroup.WithContext(ctx)
	for _, result := range results {
		if result.Value == nil || result.Value.Type != "Service" {
			return fmt.Errorf("%s did not return a Service", result.Artifact.URI)
		}
		jobs.Go(func() error {
			_, err := core.Ref[*core.Service](core.NewQuery(dag), result.Value.ID).Start(ctx)
			return err
		})
	}
	if err := jobs.Wait(); err != nil {
		return err
	}
	reportStartedWithoutProcess()
	return nil
}
