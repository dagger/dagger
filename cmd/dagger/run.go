package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:                   "run [command]",
	Aliases:               []string{"r"},
	DisableFlagsInUseLine: true,
	Long:                  "Runs the specified command in a Dagger session\n\nDAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN will be convieniently injected automatically.",
	Short:                 "Runs a command in a Dagger session",
	Example: `
dagger run -- sh -c 'curl \
-u $DAGGER_SESSION_TOKEN: \
-H "content-type:application/json" \
-d "{\"query\":\"{container{id}}\"}" http://127.0.0.1:$DAGGER_SESSION_PORT/query'`,
	Run:  Run,
	Args: cobra.MinimumNArgs(1),
}

func Run(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	listening := make(chan string)
	go func() {
		// allocate the next available port
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		listening <- fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
		if err := withEngine(ctx, sessionToken.String(), func(ctx context.Context, r *router.Router) error {
			return http.Serve(l, r) //nolint:gosec
		}); err != nil {
			panic(err)
		}
	}()

	listenPort := <-listening
	os.Setenv("DAGGER_SESSION_PORT", listenPort)
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())

	c := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	c.Run()
}
