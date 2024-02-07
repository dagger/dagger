package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/rs/cors"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
)

var (
	listenAddress string
	disableHostRW bool
	allowCORS     bool
)

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	RunE:    optionalModCmdWrapper(Listen, os.Getenv("DAGGER_SESSION_TOKEN")),
	Hidden:  true,
	Short:   "Starts the engine server",
}

func init() {
	listenCmd.Flags().StringVarP(&listenAddress, "listen", "", "127.0.0.1:8080", "Listen on network address ADDR")
	listenCmd.Flags().BoolVar(&disableHostRW, "disable-host-read-write", false, "disable host read/write access")
	listenCmd.Flags().BoolVar(&allowCORS, "allow-cors", false, "allow Cross-Origin Resource Sharing (CORS) requests")
}

func Listen(ctx context.Context, engineClient *client.Client, _ *dagger.Module, cmd *cobra.Command, _ []string) error {
	rec := progrock.FromContext(ctx)

	var stderr io.Writer
	if silent {
		stderr = os.Stderr
	} else {
		vtx := rec.Vertex(idtui.PrimaryVertex, cmd.CommandPath())
		stderr = vtx.Stderr()
	}

	sessionL, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("session listen: %w", err)
	}
	defer sessionL.Close()

	var handler http.Handler = engineClient
	if allowCORS {
		handler = cors.AllowAll().Handler(handler)
	}

	srv := &http.Server{
		Handler: handler,
		// Gosec G112: prevent slowloris attacks
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		fmt.Fprintln(stderr, "==> server shutting down")
		srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(stderr, "==> server listening on http://%s/query\n", listenAddress)

	return srv.Serve(sessionL)
}
