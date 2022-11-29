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
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/tracing"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var (
	configPath string
	workdir    string
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
	RunE: EngineSession,
}

func EngineSession(cmd *cobra.Command, args []string) error {
	startOpts := &engine.Config{
		Workdir:    workdir,
		ConfigPath: configPath,
		LogOutput:  os.Stderr,
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	randomUUID, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	sessionID := randomUUID.String()

	l, sockName, cleanupListener, err := createListener(sessionID)
	if err != nil {
		return err
	}
	defer cleanupListener()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// shutdown if requested via signal
	go func() {
		defer cancel()
		defer l.Close()
		<-signalCh
	}()

	// shutdown if our parent closes stdin
	go func() {
		defer cancel()
		defer l.Close()
		io.Copy(io.Discard, os.Stdin)
	}()

	err = engine.Start(ctx, startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		go func() {
			if _, err := fmt.Fprintf(os.Stdout, "%s\n", sockName); err != nil {
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
		return err
	}
	return nil
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
