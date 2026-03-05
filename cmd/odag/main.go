package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/internal/odag/cloudpull"
	"github.com/dagger/dagger/internal/odag/server"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/spf13/cobra"
)

const (
	defaultListenAddr = "127.0.0.1:5454"
	defaultDBPath     = ".odag/odag.db"
	odagServerEnvVar  = "ODAG_SERVER"
	runInterruptGrace = 3 * time.Second
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "odag",
		Short: "ODAG trace visualization backend and tooling",
	}

	root.AddCommand(newServeCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newFetchCmd())

	return root
}

func newServeCmd() *cobra.Command {
	var listenAddr string
	var dbPath string
	var devMode bool
	var webDir string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the ODAG backend server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			srv, err := server.New(server.Config{
				ListenAddr: listenAddr,
				DBPath:     dbPath,
				DevMode:    devMode,
				WebDir:     webDir,
			})
			if err != nil {
				return err
			}

			webURL := "http://" + listenAddr
			fmt.Fprintf(cmd.ErrOrStderr(), "otel endpoint: %s\n", webURL)
			fmt.Fprintf(cmd.ErrOrStderr(), "web interface: %s\n", webURL)

			return srv.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", defaultListenAddr, "HTTP listen address")
	cmd.Flags().StringVar(&dbPath, "db", defaultDBPath, "path to sqlite database")
	cmd.Flags().BoolVar(&devMode, "dev", false, "serve web UI from local files with automatic browser reload on changes")
	cmd.Flags().StringVar(&webDir, "web-dir", server.DefaultDevWebDir, "web UI directory used when --dev is enabled")

	return cmd
}

func newRunCmd() *cobra.Command {
	serverURL := defaultRunServerURL()
	inheritTraceContext := false

	cmd := &cobra.Command{
		Use:          "run <command> [args...]",
		Short:        "Run a command with ODAG OTEL interception",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			healthz := serverURL + "/healthz"
			healthReq, err := http.NewRequestWithContext(runCtx, http.MethodGet, healthz, nil)
			if err != nil {
				return fmt.Errorf("build health check request: %w", err)
			}
			resp, err := http.DefaultClient.Do(healthReq) //nolint:gosec
			if err != nil {
				return fmt.Errorf("odag server unavailable at %s (run `odag serve`): %w", serverURL, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("odag server unhealthy (%s), got status %d", serverURL, resp.StatusCode)
			}

			sub := exec.Command(args[0], args[1:]...) //nolint:gosec
			sub.Stdin = os.Stdin
			sub.Stdout = os.Stdout
			sub.Stderr = os.Stderr
			sub.Env = append(runEnv(os.Environ(), inheritTraceContext),
				"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
				fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=%s/v1/traces", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=%s/v1/logs", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=%s/v1/metrics", serverURL),
				"OTEL_EXPORTER_OTLP_TRACES_LIVE=1",
			)

			if err := sub.Start(); err != nil {
				return err
			}

			waitCh := make(chan error, 1)
			go func() {
				waitCh <- sub.Wait()
			}()

			select {
			case err := <-waitCh:
				return runExitErr(err)
			case <-runCtx.Done():
				interruptProcess(sub.Process)
			}

			select {
			case err := <-waitCh:
				return runExitErr(err)
			case <-time.After(runInterruptGrace):
				killProcess(sub.Process)
			}

			err = <-waitCh
			if err == nil {
				return cmdErrf(130)
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if exitErr.ExitCode() == -1 {
					return cmdErrf(130)
				}
				return cmdErrf(exitErr.ExitCode())
			}
			if errors.Is(runCtx.Err(), context.Canceled) {
				return cmdErrf(130)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", serverURL, "ODAG server base URL (default: $ODAG_SERVER or http://127.0.0.1:5454)")
	cmd.Flags().BoolVar(&inheritTraceContext, "inherit-trace-context", false, "inherit TRACEPARENT/TRACESTATE/BAGGAGE from parent process")
	return cmd
}

func defaultRunServerURL() string {
	if fromEnv := strings.TrimSpace(os.Getenv(odagServerEnvVar)); fromEnv != "" {
		return fromEnv
	}
	return "http://" + defaultListenAddr
}

func newFetchCmd() *cobra.Command {
	var dbPath string
	var orgName string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "fetch <trace-id>",
		Short: "Import a completed trace from Dagger Cloud into the local ODAG store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]

			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			res, err := cloudpull.PullTrace(cmd.Context(), st, traceID, cloudpull.PullOptions{
				OrgName: orgName,
				Timeout: timeout,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "imported trace %s: %d batches, %d span updates\n", res.TraceID, res.Batches, res.SpanUpdates)
			fmt.Fprintf(cmd.ErrOrStderr(), "start `odag serve` and open http://%s\n", defaultListenAddr)
			return nil
		},
	}

	cmd.Flags().StringVar(&dbPath, "db", defaultDBPath, "path to sqlite database")
	cmd.Flags().StringVar(&orgName, "org", "", "Dagger Cloud org name (defaults to current org)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "max time to wait for cloud trace stream")

	return cmd
}

type cmdExitError struct {
	code int
}

func (e cmdExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

func cmdErrf(code int) error {
	if code == 0 {
		return nil
	}
	return cmdExitError{code: code}
}

func runExitErr(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code == -1 {
			code = 130
		}
		return cmdErrf(code)
	}
	return err
}

func interruptProcess(p *os.Process) {
	if p == nil {
		return
	}
	_ = p.Signal(os.Interrupt)
}

func killProcess(p *os.Process) {
	if p == nil {
		return
	}
	_ = p.Kill()
}

func runEnv(base []string, inheritTraceContext bool) []string {
	if inheritTraceContext {
		return base
	}

	strip := map[string]struct{}{
		"TRACEPARENT":       {},
		"TRACESTATE":        {},
		"BAGGAGE":           {},
		"OTEL_TRACE_PARENT": {},
		"OTEL_TRACE_STATE":  {},
	}

	out := make([]string, 0, len(base))
	for _, kv := range base {
		key, _, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		if _, blocked := strip[strings.ToUpper(key)]; blocked {
			continue
		}
		out = append(out, kv)
	}
	return out
}
