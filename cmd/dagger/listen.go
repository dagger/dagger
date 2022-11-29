package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
)

const outputPrefix = "==> server listening on "

var (
	listenAddress   string
	listenLocalDirs []string
)

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	Run:     Listen,
	Hidden:  true,
	Short:   "Starts the engine server",
}

func Listen(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := withEngine(ctx, "", listenLocalDirs, func(ctx context.Context, r *router.Router) error {
		fmt.Fprintf(cmd.OutOrStderr(), "%s %s\n", outputPrefix, listenAddress)
		return http.ListenAndServe(listenAddress, r)
	}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	listenCmd.Flags().StringVarP(&listenAddress, "listen", "", ":8080", "Listen on network address ADDR")
	listenCmd.Flags().StringSliceVar(&listenLocalDirs, "local-dirs", []string{}, "local directories to allow for syncing into containers")
}
