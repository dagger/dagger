package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"go.dagger.io/dagger/engine"
	"golang.org/x/term"
)

var doCmd = &cobra.Command{
	Use:  "do",
	Run:  Do,
	Args: cobra.MaximumNArgs(1), // operation can be specified
}

func Do(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	var operation string
	if len(args) > 0 {
		operation = args[0]
	}

	vars := getKVInput(queryVarsInput)

	localDirs := getKVInput(localDirsInput)

	startOpts := &engine.Config{
		LocalDirs:  localDirs,
		Workdir:    workdir,
		ConfigPath: configPath,
	}

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

	var result []byte
	err := engine.Start(ctx, startOpts, func(ctx engine.Context) error {
		for name, id := range ctx.LocalDirs {
			vars[name] = string(id)
		}

		res := make(map[string]interface{})
		resp := &graphql.Response{Data: &res}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query:     operations,
				Variables: vars,
				OpName:    operation,
			},
			resp,
		)
		if err != nil {
			return err
		}
		if len(resp.Errors) > 0 {
			return resp.Errors
		}

		result, err = json.MarshalIndent(res, "", "    ")
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", result)
}

func getKVInput(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		m[split[0]] = split[1]
	}
	return m
}
