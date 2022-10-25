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

	result, err := doQuery(ctx, operations, operation, vars, localDirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", result)
}

func doQuery(ctx context.Context, query, op string, vars map[string]string, localDirs map[string]string) ([]byte, error) {
	opts := []dagger.ClientOpt{
		dagger.WithWorkdir(workdir),
		dagger.WithConfigPath(configPath),
	}
	for id, path := range localDirs {
		opts = append(opts, dagger.WithLocalDir(id, path))
	}

	c, err := dagger.Connect(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	for hostID := range localDirs {
		directoryID, err := c.
			Host().
			Directory(dagger.HostDirectoryID(hostID)).
			Read().
			ID(ctx)
		if err != nil {
			return nil, err
		}
		vars[hostID] = string(directoryID)
	}

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
