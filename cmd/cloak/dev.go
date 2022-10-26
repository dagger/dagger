package main

import (
	"context"
	"fmt"
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
		Workdir:    workdir,
		ConfigPath: configPath,
		LogOutput:  os.Stderr,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Addr:              fmt.Sprintf(":%d", devServerPort),
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}
		fmt.Fprintf(os.Stderr, "==> dev server listening on http://localhost:%d", devServerPort)
		return srv.ListenAndServe()
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
