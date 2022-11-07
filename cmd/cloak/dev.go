package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use: "dev",
	Run: Dev,
}

func Dev(cmd *cobra.Command, args []string) {
	startOpts := &engine.Config{
		LogOutput:     os.Stderr,
		DisableHostRW: disableHostRW,
	}

	ctx := context.Background()

	err := engine.Start(ctx, startOpts, func(ctx context.Context, r *router.Router) error {
		httpSrv := http.Server{
			Addr:              fmt.Sprintf(":%d", devServerPort),
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		httpL, err := net.Listen("tcp", fmt.Sprintf(":%d", devServerPort))
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "==> dev HTTP server listening on http://%s\n", httpL.Addr())

		if err := httpSrv.Serve(httpL); err != nil {
			return fmt.Errorf("failed to serve HTTP: %w", err)
		}

		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
