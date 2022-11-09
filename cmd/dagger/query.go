package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var queryCmd = &cobra.Command{
	Use:                   "query [flags] [operation]",
	Aliases:               []string{"q"},
	DisableFlagsInUseLine: true,
	Long:                  "Send API queries to a dagger engine\n\nWhen no document file, read query from standard input.",
	Short:                 "Send API queries to a dagger engine",
	Example: `
dagger query <<EOF
{
  container {
    from(address:"hello-world") {
      exec(args:["/hello"]) {
        stdout {
          contents
        }
      }
    }
  }
}
EOF
`,
	Run:  Query,
	Args: cobra.MaximumNArgs(1), // operation can be specified
}

func Query(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	var operation string
	if len(args) > 0 {
		operation = args[0]
	}

	vars := getKVInput(queryVarsInput)

	// Use the provided query file if specified
	// Otherwise, if stdin is a pipe or other non-tty thing, read from it.
	// Finally, default to the operations returned by the loadExtension query
	var operations string
	if queryFile != "" {
		inBytes, err := os.ReadFile(queryFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		operations = string(inBytes)
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		inBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		operations = string(inBytes)
	}

	result, err := doQuery(ctx, operations, operation, vars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", result)
}

func doQuery(ctx context.Context, query, op string, vars map[string]string) ([]byte, error) {
	opts := []dagger.ClientOpt{
		dagger.WithWorkdir(workdir),
		dagger.WithConfigPath(configPath),
	}

	if queryDebug {
		opts = append(opts, dagger.WithLogOutput(os.Stderr))
	}
	c, err := dagger.Connect(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	res := make(map[string]interface{})
	resp := &dagger.Response{Data: &res}
	err = c.Do(ctx,
		&dagger.Request{
			Query:     query,
			Variables: vars,
			OpName:    op,
		},
		resp,
	)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, resp.Errors
	}

	result, err := json.MarshalIndent(res, "", "    ")
	if err != nil {
		return nil, err
	}

	return result, nil
}

func getKVInput(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		m[split[0]] = split[1]
	}
	return m
}
