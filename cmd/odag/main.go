package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/dagger/dagger/internal/odag/server"
	"github.com/spf13/cobra"
)

const (
	defaultListenAddr = "127.0.0.1:5454"
	defaultDBPath     = ".odag/odag.db"
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

	return root
}

func newServeCmd() *cobra.Command {
	var listenAddr string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the ODAG backend server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			srv, err := server.New(server.Config{
				ListenAddr: listenAddr,
				DBPath:     dbPath,
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

	return cmd
}

func newRunCmd() *cobra.Command {
	var serverURL string

	cmd := &cobra.Command{
		Use:   "run <command> [args...]",
		Short: "Run a command with ODAG OTEL interception",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			healthz := serverURL + "/healthz"
			resp, err := http.Get(healthz) //nolint:gosec
			if err != nil {
				return fmt.Errorf("odag server unavailable at %s (run `odag serve`): %w", serverURL, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("odag server unhealthy (%s), got status %d", serverURL, resp.StatusCode)
			}

			sub := exec.CommandContext(cmd.Context(), args[0], args[1:]...) //nolint:gosec
			sub.Stdin = os.Stdin
			sub.Stdout = os.Stdout
			sub.Stderr = os.Stderr
			sub.Env = append(os.Environ(),
				"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
				fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=%s/v1/traces", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=%s/v1/logs", serverURL),
				fmt.Sprintf("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=%s/v1/metrics", serverURL),
				"OTEL_EXPORTER_OTLP_TRACES_LIVE=1",
			)

			err = sub.Run()
			if err == nil {
				return nil
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return cmdErrf(exitErr.ExitCode())
			}
			return err
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", "http://"+defaultListenAddr, "ODAG server base URL")
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
