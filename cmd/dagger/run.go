package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

var runCmd = &cobra.Command{
	Use:     "run [options] <command>...",
	Aliases: []string{"r"},
	Short:   "Run a command in a Dagger session",
	Long: strings.ReplaceAll(
		`Executes the specified command in a Dagger Session and displays
live progress in a TUI.

´DAGGER_SESSION_PORT´ and ´DAGGER_SESSION_TOKEN´ will be conveniently
injected automatically.

For example:
´´´shell
jq -n '{query:"{container{id}}"}' | \
  dagger run sh -c 'curl -s \
    -u $DAGGER_SESSION_TOKEN: \
    -H "content-type:application/json" \
    -d @- \
    http://127.0.0.1:$DAGGER_SESSION_PORT/query'
´´´`,
		"´",
		"`",
	),
	Example: strings.TrimSpace(`
dagger run go run main.go
dagger run node index.mjs
dagger run python main.py
`,
	),
	GroupID:      execGroup.ID,
	RunE:         Run,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	Annotations: map[string]string{
		printTraceLinkKey: "true",
	},
}

var waitDelay time.Duration
var runFocus bool

func init() {
	// don't require -- to disambiguate subcommand flags
	runCmd.Flags().SetInterspersed(false)

	runCmd.Flags().DurationVar(
		&waitDelay,
		"cleanup-timeout",
		10*time.Second,
		"max duration to wait between SIGTERM and SIGKILL on interrupt",
	)

	runCmd.Flags().BoolVar(&runFocus, "focus", false, "Only show output for focused commands.")
}

func Run(cmd *cobra.Command, args []string) error {
	if isPrintTraceLinkEnabled(cmd.Annotations) {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
	}

	err := run(cmd, args)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "run canceled")
			return ExitError{Code: 2}
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ExitError{Code: exitErr.ExitCode()}
		}
		return err
	}

	return nil
}

func run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	u, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}
	sessionToken := u.String()

	// Proxy OTel to the CLI and out through its locally configured exporters
	// so that it makes it to the TUI and into Cloud without having to also
	// configure the underlying command.
	otelEnv, err := setupTelemetryProxy(ctx)
	if err != nil {
		return fmt.Errorf("setup telemetry proxy: %w", err)
	}

	return withEngine(ctx, client.Params{
		SecretToken: sessionToken,
	}, func(ctx context.Context, engineClient *client.Client) error {
		sessionL, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("session listen: %w", err)
		}
		defer sessionL.Close()

		env := os.Environ()
		sessionPort := fmt.Sprintf("%d", sessionL.Addr().(*net.TCPAddr).Port)
		env = append(env, "DAGGER_SESSION_PORT="+sessionPort)
		env = append(env, "DAGGER_SESSION_TOKEN="+sessionToken)
		env = append(env, telemetry.PropagationEnv(ctx)...)
		env = append(env, otelEnv...)

		subCmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec

		subCmd.Env = env

		// allow piping to the command
		subCmd.Stdin = os.Stdin

		// NB: go run lets its child process roam free when you interrupt it, so
		// make sure they all get signalled. (you don't normally notice this in a
		// shell because Ctrl+C sends to the process group.)
		ensureChildProcessesAreKilled(subCmd)

		srv := &http.Server{ //nolint:gosec
			Handler: engineClient,
			BaseContext: func(listener net.Listener) context.Context {
				return ctx
			},
		}

		go srv.Serve(sessionL)

		var cmdErr error
		if !silent {
			if stdoutIsTTY {
				subCmd.Stdout = cmd.OutOrStdout()
			} else {
				subCmd.Stdout = os.Stdout
			}

			if stderrIsTTY {
				subCmd.Stderr = cmd.ErrOrStderr()
			} else {
				subCmd.Stderr = os.Stderr
			}

			cmdErr = subCmd.Run()
		} else {
			subCmd.Stdout = os.Stdout
			subCmd.Stderr = os.Stderr
			cmdErr = subCmd.Run()
		}

		return cmdErr
	})
}

// setupTelemetryProxy creates an OpenTelemetry proxy server and returns
// environment variables so that child processes can export to it.
func setupTelemetryProxy(ctx context.Context) ([]string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry proxy listener: %w", err)
	}

	otelProto := "http/protobuf"
	otelEndpoint := "http://" + listener.Addr().String()

	mux := http.NewServeMux()

	// Handle traces
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		body, bodyErr := io.ReadAll(r.Body)
		if bodyErr != nil {
			http.Error(w, bodyErr.Error(), http.StatusBadRequest)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if unmarshalErr := proto.Unmarshal(body, &req); unmarshalErr != nil {
			http.Error(w, unmarshalErr.Error(), http.StatusBadRequest)
			return
		}

		spans := telemetry.SpansFromPB(req.ResourceSpans)
		forwarder := telemetry.SpanForwarder{Processors: telemetry.SpanProcessors}
		if exportErr := forwarder.ExportSpans(r.Context(), spans); exportErr != nil {
			http.Error(w, exportErr.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	// Handle logs
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		body, bodyErr := io.ReadAll(r.Body)
		if bodyErr != nil {
			http.Error(w, bodyErr.Error(), http.StatusBadRequest)
			return
		}
		var req collogspb.ExportLogsServiceRequest
		if unmarshalErr := proto.Unmarshal(body, &req); unmarshalErr != nil {
			http.Error(w, unmarshalErr.Error(), http.StatusBadRequest)
			return
		}
		forwarder := telemetry.LogForwarder{Processors: telemetry.LogProcessors}
		if exportErr := telemetry.ReexportLogsFromPB(r.Context(), forwarder, &req); exportErr != nil {
			http.Error(w, exportErr.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	// Handle metrics
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		body, bodyErr := io.ReadAll(r.Body)
		if bodyErr != nil {
			http.Error(w, bodyErr.Error(), http.StatusBadRequest)
			return
		}
		var req colmetricspb.ExportMetricsServiceRequest
		if unmarshalErr := proto.Unmarshal(body, &req); unmarshalErr != nil {
			http.Error(w, unmarshalErr.Error(), http.StatusBadRequest)
			return
		}
		if exportErr := enginetel.ReexportMetricsFromPB(r.Context(), telemetry.MetricExporters, &req); exportErr != nil {
			http.Error(w, exportErr.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	// Start serving in the background
	server := &http.Server{Handler: mux}

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Warn("failed to serve telemetry proxy", "error", serveErr)
		}
	}()

	// Clean up when the context is canceled
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	return []string{
		engine.OTelExporterProtocolEnv + "=" + otelProto,
		engine.OTelExporterEndpointEnv + "=" + otelEndpoint,
		engine.OTelTracesProtocolEnv + "=" + otelProto,
		engine.OTelTracesEndpointEnv + "=" + otelEndpoint + "/v1/traces",
		// Indicate that the /v1/trace endpoint accepts live telemetry.
		engine.OTelTracesLiveEnv + "=1",
		// Dagger sets up log+metric exporters too. Explicitly set them
		// so things can detect support for it.
		engine.OTelLogsProtocolEnv + "=" + otelProto,
		engine.OTelLogsEndpointEnv + "=" + otelEndpoint + "/v1/logs",
		engine.OTelMetricsProtocolEnv + "=" + otelProto,
		engine.OTelMetricsEndpointEnv + "=" + otelEndpoint + "/v1/metrics",
	}, nil
}
