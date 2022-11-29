package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:                   "run [command]",
	Aliases:               []string{"r"},
	DisableFlagsInUseLine: true,
	Long:                  "Runs the specified command in a Dagger session\n\nDAGGER_SESSION_URL and DAGGER_SESSION_TOKEN will be convieniently injected automatically.",
	Short:                 "Runs a command in a Dagger session",
	Example: `
dagger run -- sh -c 'curl \
-u $DAGGER_SESSION_TOKEN: \
-H "content-type:application/json" \
-d "{\"query\":\"{container{id}}\"}" $DAGGER_SESSION_URL'`,
	Run:  Run,
	Args: cobra.MinimumNArgs(1),
}

func Run(cmd *cobra.Command, args []string) {
	rand.Seed(time.Now().UnixNano())
	ctx := context.Background()
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	listening := make(chan string)
	go func() {
		// allocate the next available port
		l, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		listening <- fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
		if err := withEngine(ctx, sessionToken.String(), []string{"/"}, func(ctx context.Context, r *router.Router) error {
			return http.Serve(l, r)
		}); err != nil {
			panic(err)
		}
	}()

	listenPort := <-listening
	os.Setenv("DAGGER_SESSION_URL", fmt.Sprintf("http://localhost:%s/query", listenPort))
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())

	c := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	c.Run()
}
