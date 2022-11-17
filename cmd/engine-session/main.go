package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/tracing"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string

	// this variable gets set at build time.
	buildRemoteEngineAddr = fmt.Sprintf("docker-image://ghcr.io/dagger/engine:%s", buildRevision())
)

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "")
	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "")
}

var rootCmd = &cobra.Command{
	Use: "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger engine. It is not intended to be used by humans directly.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		return err
	},
	Run: EngineSession,
}

func EngineSession(cmd *cobra.Command, args []string) {
	remoteEngineAddr := buildRemoteEngineAddr
	if dh := os.Getenv("DAGGER_RUNNER_HOST"); dh != "" {
		remoteEngineAddr = dh
	}
	startOpts := &engine.Config{
		Workdir:    workdir,
		ConfigPath: configPath,
		LogOutput:  os.Stderr,
		RemoteAddr: remoteEngineAddr,
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}

	// shutdown if requested via signal
	go func() {
		<-signalCh
		l.Close()
	}()

	// shutdown if our parent closes stdin
	go func() {
		io.Copy(io.Discard, os.Stdin)
		l.Close()
	}()

	port := l.Addr().(*net.TCPAddr).Port

	err = engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		// TODO: still kind of racy, client should retry connections a few times
		go func() {
			if _, err := os.Stdout.Write([]byte(fmt.Sprintf("%d\n", port))); err != nil {
				panic(err)
			}
		}()

		err := srv.Serve(l)
		// if error is "use of closed network connection", it's expected
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}

func buildRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}

	return ""
}
