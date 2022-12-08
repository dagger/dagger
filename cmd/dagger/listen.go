package main

import (
	"context"
	"fmt"
	"net"
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

func listenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "listen",
		Aliases: []string{"l"},
		Run:     Listen,
		Hidden:  true,
		Short:   "Starts the engine server",
	}

	cmd.Flags().StringVarP(&listenAddress, "listen", "", ":8080", "Listen on network address ADDR")
	cmd.Flags().StringSliceVar(&listenLocalDirs, "local-dirs", []string{}, "local directories to allow for syncing into containers")

	return cmd
}

func Listen(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := withEngine(ctx, "", listenLocalDirs, func(ctx context.Context, r *router.Router) error {
		l, err := net.Listen("tcp", listenAddress)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			os.Exit(1)
		}
		listenAddress = l.Addr().(*net.TCPAddr).String() // handles case where port is :0 and selected automatically

		fmt.Fprintf(cmd.OutOrStderr(), "%s %s\n", outputPrefix, listenAddress)
		return http.Serve(l, r)
	}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
		os.Exit(1)
	}
}
