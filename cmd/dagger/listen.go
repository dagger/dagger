package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
)

var listenAddress string

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	Run:     Listen,
	Hidden:  true,
	Short:   "Starts the engine server",
}

func Listen(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := withEngine(ctx, "", func(ctx context.Context, r *router.Router) error {
		fmt.Fprintf(os.Stderr, "==> server listening on %s\n", listenAddress)
		return http.ListenAndServe(listenAddress, r)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	listenCmd.Flags().StringVarP(&listenAddress, "listen", "", ":8080", "Listen on network address ADDR")
}
