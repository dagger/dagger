package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func sessionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "session",
		Long:         "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger engine. It is not intended to be used by humans directly.",
		Hidden:       true,
		RunE:         EngineSession,
		SilenceUsage: true,
	}
}

type connectParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

func EngineSession(cmd *cobra.Command, args []string) error {
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return err
	}

	startOpts := &engine.Config{
		Workdir:      workdir,
		ConfigPath:   configPath,
		LogOutput:    os.Stderr,
		RunnerHost:   internalengine.RunnerHost(),
		SessionToken: sessionToken.String(),
		JournalFile:  os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
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

	return engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		paramBytes, err := json.Marshal(connectParams{
			Port:         port,
			SessionToken: sessionToken.String(),
		})
		if err != nil {
			return err
		}
		paramBytes = append(paramBytes, '\n')
		go func() {
			if _, err := os.Stdout.Write(paramBytes); err != nil {
				panic(err)
			}
		}()

		err = srv.Serve(l)
		// if error is "use of closed network connection", it's expected
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
}
