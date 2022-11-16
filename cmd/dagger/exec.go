package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var execCmd = &cobra.Command{
	Use:                   "exec [command]",
	Aliases:               []string{"x"},
	DisableFlagsInUseLine: true,
	Long:                  "Executes the specified command in a Dagger session\n\nDAGGER_SESSION_URL will be convieniently injected automatically.",
	Short:                 "Executes a command in a Dagger session",
	Example: `
dagger exec -- sh -c 'curl \
-H "content-type:application/json" \
-d "{\"query\":\"{container{id}}\"}" \
http://$DAGGER_SESSION_URL/query'`,
	Run:  Exec,
	Args: cobra.MinimumNArgs(1),
}

func Exec(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := setupServer(ctx); err != nil {
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
		http.Serve(l, nil)
	}()

	listenPort := <-listening
	os.Setenv("DAGGER_SESSION_URL", fmt.Sprintf("http://localhost:%s", listenPort))

	c := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	c.Run()
}
