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

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	Run:     Listen,
	Short:   "Starts the engine server",
}

func Listen(cmd *cobra.Command, args []string) {
	startOpts := &engine.Config{
		Workdir:       workdir,
		ConfigPath:    configPath,
		LogOutput:     os.Stderr,
		DisableHostRW: disableHostRW,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		l, err := net.Listen("tcp", listenAddress)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "==> server listening on %s", l.Addr())

		return srv.Serve(l)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// func startEngine(conf *engine.Config) error {
// return nil
// }
