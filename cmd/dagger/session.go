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

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/telemetry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/vito/progrock/console"
)

var sessionLabels pipeline.Labels

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "session",
		Long:         "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger Engine. It is not intended to be used by humans directly.",
		Hidden:       true,
		RunE:         EngineSession,
		SilenceUsage: true,
	}
	cmd.Flags().Var(&sessionLabels, "label", "label that identifies the source of this session (e.g, --label 'dagger.io/sdk.name:python' --label 'dagger.io/sdk.version:0.5.2' --label 'dagger.io/sdk.async:true')")
	return cmd
}

type connectParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

func EngineSession(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return err
	}

	labels := &sessionLabels

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

	runnerHost, err := engine.RunnerHost()
	if err != nil {
		return err
	}

	sess, _, err := client.Connect(ctx, client.Params{
		SecretToken:    sessionToken.String(),
		RunnerHost:     runnerHost,
		UserAgent:      labels.AppendCILabel().AppendAnonymousGitLabels(workdir).String(),
		ProgrockWriter: telemetry.NewLegacyIDInternalizer(console.NewWriter(os.Stderr)),
		JournalFile:    os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
	})
	if err != nil {
		return err
	}
	defer sess.Close()

	srv := http.Server{
		Handler:           sess,
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
}
