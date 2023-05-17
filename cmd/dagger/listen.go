package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
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
		fmt.Fprintf(os.Stderr, "==> server listening on http://%s/query\n", listenAddress)
		return http.ListenAndServe(listenAddress, r) //nolint:gosec
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
