package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

var (
	sessionLabels  = enginetel.NewLabelFlag()
	sessionVersion string
)

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "session [options]",
		Long:         "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger Engine. It is not intended to be used by humans directly.",
		Hidden:       true,
		RunE:         EngineSession,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&sessionVersion, "version", "", "")
	// This is not used by kept for backward compatibility.
	// We don't want SDKs failing because this flag is not defined.
	cmd.Flags().Var(&sessionLabels, "label", "label that identifies the source of this session (e.g, --label 'dagger.io/sdk.name:python' --label 'dagger.io/sdk.version:0.5.2' --label 'dagger.io/sdk.async:true')")
	return cmd
}

type connectParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

func EngineSession(cmd *cobra.Command, args []string) error {
	// discard SIGPIPE, which can happen when stdout or stderr are closed
	// (possibly from the spawning process going away)
	//
	// see https://pkg.go.dev/os/signal#hdr-SIGPIPE for more info
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)

	ctx := cmd.Context()

	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return err
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

	return withEngine(ctx, client.Params{
		SecretToken: sessionToken.String(),
		Version:     sessionVersion,
	}, func(ctx context.Context, sess *client.Client) error {
		// Requests maintain their original trace context from the client, rather
		// than appearing beneath the dagger session span, so in order to see any
		// logs we need to reveal everything.
		Frontend.RevealAllSpans()

		srv := http.Server{
			Handler:           sess,
			ReadHeaderTimeout: 30 * time.Second,
			BaseContext: func(net.Listener) context.Context {
				return ctx
			},
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
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				l.Close()
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
