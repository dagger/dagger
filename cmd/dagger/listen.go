package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
)

var (
	listenAddress string
	disableHostRW bool
)

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	Run:     Listen,
	Hidden:  true,
	Short:   "Starts the engine server",
}

func init() {
	listenCmd.Flags().StringVarP(&listenAddress, "listen", "", "127.0.0.1:8080", "Listen on network address ADDR")
	listenCmd.Flags().BoolVar(&disableHostRW, "disable-host-read-write", false, "disable host read/write access")
}

func Listen(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := withEngineAndTUI(ctx, engine.Config{}, func(ctx context.Context, r *router.Router) error {
		rec := progrock.RecorderFromContext(ctx)

		var stderr io.Writer
		if silent {
			stderr = os.Stderr
		} else {
			vtx := rec.Vertex("listen", "listen")
			stderr = vtx.Stderr()
		}

		sessionL, err := net.Listen("tcp", listenAddress)
		if err != nil {
			return fmt.Errorf("session listen: %w", err)
		}
		defer sessionL.Close()

		srv := &http.Server{Handler: r}

		go func() {
			<-ctx.Done()
			fmt.Fprintln(stderr, "==> server shutting down")
			srv.Shutdown(context.Background())
		}()

		fmt.Fprintf(stderr, "==> server listening on http://%s/query\n", listenAddress)

		return srv.Serve(sessionL) // nolint:gosec
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
